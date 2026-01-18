package mqtt

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"maps"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"go.uber.org/zap"
)

// PeerMQTT implements the source and sink functionality for MQTT
type PeerMQTT struct {
	*Client
	Config        Config
	topicRewriter *TopicRewriter
}

type Config struct {
	Servers     []string `json:"servers"`
	TopicPrefix string   `json:"topicPrefix"`
	// ClientOptions. Somewhat similar to github.com/eclipse/paho.mqtt.golang.ClientOptions
	ClientOptions `json:"clientOptions"`
	// TopicToFields is used to convert/map MQTT topic segments to fields eg postgres table columns
	TopicToFields []TopicToField     `json:"topicToFields,omitempty"`
	TopicRewrites []TopicRewriteRule `json:"topicRewrites,omitempty"`
}

func (p *PeerMQTT) Connect(config json.RawMessage, args ...any) error {
	var cfg Config

	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal MQTT config: %w", err)
	}

	opts := cfg.ClientOptions

	for _, server := range cfg.Servers {
		u, err := url.Parse(server)
		if err != nil {
			return fmt.Errorf("failed to parse server URL %s: %w", server, err)
		}
		opts.Servers = append(opts.Servers, u)
	}

	p.Config.TopicPrefix = cmp.Or(cfg.TopicPrefix, "pigo")
	p.Config.TopicToFields = cfg.TopicToFields
	p.Config.TopicRewrites = cfg.TopicRewrites

	if len(cfg.TopicRewrites) > 0 {
		var err error
		p.topicRewriter, err = NewTopicRewriter(cfg.TopicRewrites)
		if err != nil {
			return fmt.Errorf("failed to initialize topic rewriter: %w", err)
		}
	}

	mqttOpts, err := convertToPahoOptions(&opts) // Now returns error
	if err != nil {
		return fmt.Errorf("failed to convert MQTT options: %w", err)
	}

	setDefaultOptions(mqttOpts)

	p.Client = NewClient(mqttOpts)

	if err := p.Client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", err)
	}

	return nil
}

func (p *PeerMQTT) Pub(event cdc.Event, args ...any) error {
	topic := fmt.Sprintf("%s/%s/%s/%s", p.Config.TopicPrefix, event.Payload.Source.Schema, event.Payload.Source.Table, event.Payload.Op)
	data, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	return p.Client.Publish(topic, 0, false, data)
}

func (p *PeerMQTT) Sub(args ...any) (<-chan cdc.Event, error) {
	prefix := p.Config.TopicPrefix

	if len(args) > 0 {
		if prefixArg, ok := args[0].(string); ok {
			prefix = strings.Trim(prefixArg, "/")
		}
	}

	filter := prefix + "/#"
	events := make(chan cdc.Event, 100)

	token := p.Client.client.Subscribe(filter, 0, func(_ mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		originalTopic := topic
		topicParts := strings.Split(topic, "/")
		prefixParts := strings.Split(prefix, "/")

		// check if topic follows the standard format
		if len(topicParts) < len(prefixParts)+3 {
			// fallback: check for topic rewrite match
			if p.topicRewriter != nil {
				rewrittenTopic := p.topicRewriter.RewriteTopic(topic, ActionSubscribe, "", "")
				if rewrittenTopic != topic {
					// topic matches rewrite, use the "to" of matched topic for processing
					topic = rewrittenTopic
					topicParts = strings.Split(topic, "/")

					p.logger.Debug("topic rewritten for constrained device",
						zap.String("original", originalTopic),
						zap.String("rewritten", topic))
				} else {
					p.logger.Warn("invalid topic format and no rewrite rule matched",
						zap.String("topic", originalTopic))
					return
				}
			} else {
				p.logger.Warn("invalid topic format", zap.String("topic", originalTopic))
				return
			}
		}

		// verify the rewritten topic still has the correct format
		if len(topicParts) < len(prefixParts)+3 {
			p.logger.Warn("rewritten topic still has invalid format",
				zap.String("original", originalTopic),
				zap.String("rewritten", topic))
			return
		}

		parts := topicParts[len(prefixParts):]
		schema, table := parts[0], parts[1]

		operation := parts[2]
		var opCode string
		switch operation {
		case "c", "create":
			opCode = string(cdc.OpCreate)
		case "u", "update":
			opCode = string(cdc.OpUpdate)
		case "d", "delete":
			opCode = string(cdc.OpDelete)
		case "r", "read":
			opCode = string(cdc.OpRead)
		default:
			p.logger.Warn("unknown operation", zap.String("operation", operation))
			return
		}

		// topic formats and payload types
		var payloads []any
		var isScalarPayload bool
		var scalarFieldName string

		// check for batch operation first
		isBatch := false
		conditionsStartIdx := 3
		if len(parts) > 3 && parts[3] == "batch" {
			isBatch = true
			conditionsStartIdx = 4
		}

		// check for scalar payload format .../operation/[key1/val1/.../]scalar_field_name
		if len(parts) > conditionsStartIdx {
			// an odd number of remaining parts indicates scalar field at end
			remainingParts := parts[conditionsStartIdx:]
			if len(remainingParts)%2 == 1 {
				isScalarPayload = true
				scalarFieldName = remainingParts[len(remainingParts)-1]
			}
		}

		// extract key-value pairs from topic (excluding the scalar field name if present)
		conditions := make(map[string]any)
		if len(parts) > conditionsStartIdx {
			endIdx := len(parts)
			if isScalarPayload {
				endIdx = len(parts) - 1 // exclude the scalar field name
			}

			for i := conditionsStartIdx; i < endIdx; i += 2 {
				if i+1 < endIdx {
					key := parts[i]
					value := parts[i+1]
					// try to parse the value as different types
					conditions[key] = parseScalarValue(value)
				}
			}
		}

		// process payload based on type
		if len(msg.Payload()) > 0 {
			if isScalarPayload {
				scalarValue := parseScalarValue(string(msg.Payload()))
				payload := make(map[string]any)

				maps.Copy(payload, conditions)

				payload[scalarFieldName] = scalarValue

				payloads = []any{payload}

				p.logger.Debug("processed scalar payload",
					zap.String("topic", topic),
					zap.String("field", scalarFieldName),
					zap.Any("value", scalarValue),
					zap.Any("conditions", conditions))

			} else {
				// handle JSON payload
				if isBatch {
					if err := json.Unmarshal(msg.Payload(), &payloads); err != nil {
						var singlePayload any
						if err := json.Unmarshal(msg.Payload(), &singlePayload); err != nil {
							p.logger.Warn("invalid JSON payload",
								zap.Error(err),
								zap.String("topic", topic))
							return
						}
						payloads = []any{singlePayload}
					}
				} else {
					var singlePayload any
					if err := json.Unmarshal(msg.Payload(), &singlePayload); err != nil {
						p.logger.Warn("invalid JSON payload",
							zap.Error(err),
							zap.String("topic", topic))
						return
					}
					payloads = []any{singlePayload}
				}

				// merge topic conditions into JSON payload if it's a map
				if len(conditions) > 0 {
					for i, payload := range payloads {
						if payloadMap, ok := payload.(map[string]any); ok {
							// add conditions from topic, but don't overwrite existing fields
							for key, value := range conditions {
								if _, exists := payloadMap[key]; !exists {
									payloadMap[key] = value
								}
							}
							payloads[i] = payloadMap
						}
					}
				}
			}
		} else if isScalarPayload {
			// no payload but we expect a scalar - create empty payload with topic data
			payload := make(map[string]any)
			maps.Copy(payload, conditions)
			// set scalar field to null since no payload was provided
			payload[scalarFieldName] = nil
			payloads = []any{payload}
		} else if len(conditions) > 0 {
			// no payload but we have conditions from topic
			payloads = []any{conditions}
		}

		// extract fields from topic if configured
		var extractedFields map[string]any
		if len(p.Config.TopicToFields) > 0 {
			extractedFields = extractFieldsFromTopic(topic, p.Config.TopicToFields)
		}

		// process each payload
		for _, payload := range payloads {
			// create events with extracted fields from the topic
			event := createEventWithFieldsFromTopic(schema, table, opCode, payload, extractedFields)

			select {
			case events <- event:
			default:
				p.logger.Warn("event channel full, dropping message")
			}
		}
	})

	if err := token.Error(); err != nil {
		close(events)
		return nil, fmt.Errorf("mqtt subscribe failed: %w", err)
	}

	return events, nil
}

func (p *PeerMQTT) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePubSub
}

func (p *PeerMQTT) Disconnect() error {
	p.client.Disconnect(500)
	return nil
}

// createEvent creates a new CDC event using the builder pattern
func createEvent(schema, table, opCode string, payload any) cdc.Event {
	source := cdc.NewSourceBuilder("mqtt", "mqtt-source").
		WithDatabase("mqtt").
		WithSchema(schema).
		WithTable(table).
		WithTimestamp(time.Now().UnixMilli()).
		Build()

	builder := cdc.NewEventBuilder().
		WithSource(source).
		WithOperation(cdc.Operation(opCode)).
		WithTimestamp(time.Now().UnixMilli())

	if opCode == string(cdc.OpDelete) {
		builder.WithBefore(payload)
	} else {
		builder.WithAfter(payload)
	}

	return builder.Build()
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorMQTT, &PeerMQTT{})
}

func createEventWithFieldsFromTopic(schema, table, opCode string, payload any, extractedFields map[string]any) cdc.Event {
	// If we have extracted fields, merge them with the payload
	if len(extractedFields) > 0 && payload != nil {
		// Convert payload to map for merging
		if payloadMap, ok := payload.(map[string]any); ok {
			// Add extracted fields to payload, but don't overwrite existing fields
			for field, value := range extractedFields {
				if _, exists := payloadMap[field]; !exists {
					payloadMap[field] = value
				}
			}
			payload = payloadMap
		} else {
			fmt.Println("TODO: handle non-map payload merging") // TODO: handle non-map payload merging
		}
	} else if len(extractedFields) > 0 {
		// If no payload but we have extracted fields, use extracted fields as payload
		payload = extractedFields
	}

	// Use existing createEvent function or create the event here
	return createEvent(schema, table, opCode, payload)
}

// func parseScalarValue(value string) any {
// 	if boolVal, err := strconv.ParseBool(value); err == nil {
// 		return boolVal
// 	}
// 	if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
// 		return intVal
// 	}
// 	if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
// 		return floatVal
// 	}

// 	return value
// }

// apparently response isn't useful
/*
	enableResponse := true
	if len(args) > 1 {
		if enabled, ok := args[1].(bool); ok {
			enableResponse = enabled
		}
	}
	if enableResponse {
		responseTopic := "/response" + msg.Topic()
		response := map[string]any{
			"success":   true,
			"timestamp": time.Now().UnixMilli(),
			"count":     len(payloads),
		}

		responseData, err := json.Marshal(response)
		if err != nil {
			p.logger.Error("failed to marshal response", zap.Error(err))
			return
		}

		if err := p.Client.Publish(responseTopic, 0, false, responseData); err != nil {
			p.logger.Error("failed to publish response",
				zap.Error(err),
				zap.String("topic", responseTopic))
		}
	}
*/
