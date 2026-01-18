package nats

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/nats-io/nats.go"
)

// PeerNATS implements the source and sink for NATS
type PeerNATS struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	stream  string
	subject string
	Config  Config
}

var (
	errConnNotInitialized = errors.New("NATS connection not initialized")
)

// Config represents NATS configuration
// maybe take nats.Options as Config?
type Config struct {
	Servers       []string `json:"servers"`
	Stream        string   `json:"stream"`
	SubjectPrefix string   `json:"subjectPrefix"`
	Username      string   `json:"username,omitempty"`
	Password      string   `json:"password,omitempty"`
	TLS           struct {
		Enabled  bool   `json:"enabled"`
		CertFile string `json:"certFile,omitempty"`
		KeyFile  string `json:"keyFile,omitempty"`
		CAFile   string `json:"caFile,omitempty"`
	} `json:"tls,omitempty"`
}

// Connect establishes a connection to the NATS server
func (p *PeerNATS) Connect(config json.RawMessage, _ ...any) error {
	if err := json.Unmarshal(config, &p.Config); err != nil {
		return fmt.Errorf("unmarshal NATS config: %w", err)
	}

	// Set defaults
	if len(p.Config.Servers) == 0 {
		p.Config.Servers = []string{nats.DefaultURL}
	}
	p.Config.SubjectPrefix = cmp.Or(p.Config.SubjectPrefix, "pigo")
	p.Config.Stream = cmp.Or(p.Config.Stream, fmt.Sprintf("%s-stream", p.Config.SubjectPrefix))

	p.subject = fmt.Sprintf("%s.>", p.Config.SubjectPrefix)
	opts := defaultOptions(p.Config)

	// Connect to first available server
	var err error
	for _, server := range p.Config.Servers {
		p.nc, err = nats.Connect(server, opts...)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("connect to NATS server: %w", err)
	}

	if p.js, err = p.nc.JetStream(); err != nil {
		p.nc.Close()
		return fmt.Errorf("create JetStream context: %w", err)
	}

	if err := p.ensureStream(); err != nil {
		p.nc.Close()
		return fmt.Errorf("ensure stream: %w", err)
	}

	p.stream = p.Config.Stream
	return nil
}

// Pub publishes a CDC event to NATS
func (p *PeerNATS) Pub(event cdc.Event, _ ...any) error {
	if p.js == nil {
		return errConnNotInitialized
	}

	subject := fmt.Sprintf("%s.%s.%s.%s",
		p.Config.SubjectPrefix,
		event.Payload.Source.Schema,
		event.Payload.Source.Table,
		event.Payload.Op)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal CDC event: %w", err)
	}

	_, err = p.js.Publish(subject, data)
	if err != nil {
		return fmt.Errorf("publish message: %w", err)
	}

	return nil
}

// Sub subscribes to CDC events from NATS
func (p *PeerNATS) Sub(_ ...any) (<-chan cdc.Event, error) {
	if p.js == nil {
		return nil, errConnNotInitialized
	}

	events := make(chan cdc.Event, 100)
	consumer := fmt.Sprintf("%s-consumer", p.Config.SubjectPrefix)

	_, err := p.js.AddConsumer(p.stream, &nats.ConsumerConfig{
		Durable:       consumer,
		AckPolicy:     nats.AckExplicitPolicy,
		MaxDeliver:    3,
		AckWait:       time.Minute,
		FilterSubject: p.subject,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}

	sub, err := p.js.PullSubscribe(p.subject, consumer)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	go p.processMessages(sub, events)
	return events, nil
}

// processMessages handles subscription message processing
func (p *PeerNATS) processMessages(sub *nats.Subscription, events chan<- cdc.Event) {
	defer close(events)
	defer sub.Unsubscribe()

	for {
		msgs, err := sub.Fetch(10, nats.MaxWait(time.Second))
		if err != nil {
			if err != nats.ErrTimeout {
				log.Printf("fetch messages: %v", err)
			}
			continue
		}

		for _, msg := range msgs {
			var event cdc.Event
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				log.Printf("unmarshal message: %v", err)
				msg.Nak()
				continue
			}

			select {
			case events <- event:
				msg.Ack()
			default:
				msg.Nak() // Channel full, retry later
			}
		}
	}
}

// Type returns the connector type
func (p *PeerNATS) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePubSub
}

// Disconnect closes the NATS connection
func (p *PeerNATS) Disconnect() error {
	if p.nc != nil {
		p.nc.Close()
	}
	return nil
}

// ensureStream creates or updates the stream
func (p *PeerNATS) ensureStream() error {
	config := &nats.StreamConfig{
		Name:     p.Config.Stream,
		Subjects: []string{p.subject},
		Storage:  nats.FileStorage,
		Replicas: 1,
	}

	stream, err := p.js.StreamInfo(p.Config.Stream)
	if err == nil {
		if !streamConfigEqual(stream.Config, *config) {
			if _, err = p.js.UpdateStream(config); err != nil {
				return fmt.Errorf("update stream: %w", err)
			}
			log.Printf("Updated stream: %s", p.Config.Stream)
		}
		return nil
	}

	if err != nats.ErrStreamNotFound {
		return fmt.Errorf("get stream info: %w", err)
	}

	if _, err := p.js.AddStream(config); err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	log.Printf("Created stream: %s", p.Config.Stream)
	return nil
}

// streamConfigEqual checks if two nats.StreamConfig are equivalent
func streamConfigEqual(a, b nats.StreamConfig) bool {
	if a.Name != b.Name || a.Storage != b.Storage || a.Replicas != b.Replicas {
		return false
	}

	if len(a.Subjects) != len(b.Subjects) {
		return false
	}

	for i := range a.Subjects {
		if a.Subjects[i] != b.Subjects[i] {
			return false
		}
	}
	return true
}

func defaultOptions(c Config) []nats.Option {
	opts := []nats.Option{
		nats.Timeout(5 * time.Second),
		nats.PingInterval(10 * time.Second),
		nats.MaxPingsOutstanding(3),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	}

	if c.Username != "" && c.Password != "" {
		opts = append(opts, nats.UserInfo(c.Username, c.Password))
	}

	if c.TLS.Enabled {
		var tlsOpt nats.Option
		if c.TLS.CAFile != "" {
			tlsOpt = nats.RootCAs(c.TLS.CAFile)
		} else if c.TLS.CertFile != "" && c.TLS.KeyFile != "" {
			tlsOpt = nats.ClientCert(c.TLS.CertFile, c.TLS.KeyFile)
		}
		if tlsOpt != nil {
			opts = append(opts, tlsOpt)
		}
	}

	return opts
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorNATS, &PeerNATS{})
}
