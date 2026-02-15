package mqtt

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/edgeflare/pigo/pkg/util/rand"
	"go.uber.org/zap"
)

// Client represents an MQTT client with PostgREST forwarding capabilities.
type Client struct {
	opts        *mqtt.ClientOptions
	client      mqtt.Client
	logger      *zap.Logger
	topicPrefix string
}

// init ensures that the logger is not nil
func (c *Client) init() {
	if c.logger == nil {
		logger, err := zap.NewProduction()
		if err != nil {
			// If we can't create a production logger, fall back to a no-op logger
			fmt.Fprintf(os.Stderr, "Failed to create default logger: %v\n", err)
			c.logger = zap.NewNop()
		} else {
			c.logger = logger
		}
	}
}

// NewClient creates a new MQTT client with the given options and logger.
func NewClient(opts *mqtt.ClientOptions, logger ...*zap.Logger) *Client {
	client := &Client{
		opts: opts,
	}

	if len(logger) > 0 {
		client.logger = logger[0]
	}

	client.init()
	return client
}

// Connect establishes a connection to the MQTT broker.
func (c *Client) Connect() error {
	c.client = mqtt.NewClient(c.opts)
	if token := c.client.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("broker connection error: %w", token.Error())
	}

	return nil
}

// Publish sends a message to the specified MQTT topic.
func (c *Client) Publish(topic string, qos byte, retained bool, payload interface{}) error {
	token := c.client.Publish(topic, qos, retained, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		c.logger.Error("Publish error", zap.Error(err))
		return err
	}
	c.logger.Debug("Message published", zap.String("topic", topic))
	return nil
}

// Disconnect closes the connection to the MQTT broker.
func (c *Client) Disconnect() {
	c.client.Disconnect(250)
	c.logger.Info("Disconnected from MQTT broker")
}

// Subscribe registers a callback for messages on the specified MQTT topic.
func (c *Client) Subscribe(topic string, qos byte, callback mqtt.MessageHandler) error {
	token := c.client.Subscribe(topic, qos, callback)
	token.Wait()
	if err := token.Error(); err != nil {
		c.logger.Error("Subscribe error", zap.Error(err), zap.String("topic", topic))
		return fmt.Errorf("subscribe error: %w", err)
	}
	c.logger.Debug("Subscribed to topic", zap.String("topic", topic))
	return nil
}

func convertToPahoOptions(opts *ClientOptions) (*mqtt.ClientOptions, error) {
	pahoOpts := mqtt.NewClientOptions()

	// Convert Servers
	for _, server := range opts.Servers {
		pahoOpts.AddBroker(server.String())
	}

	// Set other options only if they are non-empty or non-nil
	if opts.ClientID != "" {
		pahoOpts.SetClientID(opts.ClientID)
	}
	if opts.Username != "" {
		pahoOpts.SetUsername(opts.Username)
	}
	if opts.Password != "" {
		pahoOpts.SetPassword(opts.Password)
	}
	if opts.TLS != nil {
		tlsConfig, err := createTLSConfig(opts.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %w", err)
		}
		if tlsConfig != nil {
			pahoOpts.SetTLSConfig(tlsConfig)
		}
	} else if opts.TLSConfig != nil {
		pahoOpts.SetTLSConfig(opts.TLSConfig)
	}
	if opts.KeepAlive > 0 {
		pahoOpts.SetKeepAlive(time.Duration(opts.KeepAlive) * time.Second)
	}
	if opts.PingTimeout > 0 {
		pahoOpts.SetPingTimeout(opts.PingTimeout)
	}
	if opts.ConnectTimeout > 0 {
		pahoOpts.SetConnectTimeout(opts.ConnectTimeout)
	}
	if opts.MaxReconnectInterval > 0 {
		pahoOpts.SetMaxReconnectInterval(opts.MaxReconnectInterval)
	}
	if opts.ConnectRetryInterval > 0 {
		pahoOpts.SetConnectRetryInterval(opts.ConnectRetryInterval)
	}
	if opts.WriteTimeout > 0 {
		pahoOpts.SetWriteTimeout(opts.WriteTimeout)
	}
	if opts.MessageChannelDepth > 0 {
		pahoOpts.SetMessageChannelDepth(opts.MessageChannelDepth)
	}
	if opts.MaxResumePubInFlight > 0 {
		pahoOpts.SetMaxResumePubInFlight(opts.MaxResumePubInFlight)
	}

	// Set boolean options
	pahoOpts.SetCleanSession(opts.CleanSession)
	pahoOpts.SetOrderMatters(opts.Order)
	pahoOpts.SetAutoReconnect(opts.AutoReconnect)
	pahoOpts.SetConnectRetry(opts.ConnectRetry)
	pahoOpts.SetResumeSubs(opts.ResumeSubs)
	pahoOpts.SetAutoAckDisabled(opts.AutoAckDisabled)

	// Set non-primitive options only if non-nil
	if opts.Store != nil {
		pahoOpts.SetStore(opts.Store)
	}
	if opts.DefaultPublishHandler != nil {
		pahoOpts.SetDefaultPublishHandler(opts.DefaultPublishHandler)
	}
	if opts.OnConnect != nil {
		pahoOpts.SetOnConnectHandler(opts.OnConnect)
	}
	if opts.OnConnectionLost != nil {
		pahoOpts.SetConnectionLostHandler(opts.OnConnectionLost)
	}
	if opts.OnReconnecting != nil {
		pahoOpts.SetReconnectingHandler(opts.OnReconnecting)
	}
	if opts.OnConnectAttempt != nil {
		pahoOpts.SetConnectionAttemptHandler(opts.OnConnectAttempt)
	}
	if opts.HTTPHeaders != nil {
		pahoOpts.SetHTTPHeaders(opts.HTTPHeaders)
	}
	if opts.WebsocketOptions != nil {
		pahoOpts.SetWebsocketOptions(opts.WebsocketOptions)
	}
	if opts.Dialer != nil {
		pahoOpts.SetDialer(opts.Dialer)
	}
	if opts.CustomOpenConnectionFn != nil {
		pahoOpts.SetCustomOpenConnectionFn(opts.CustomOpenConnectionFn)
	}

	// Set will if enabled
	if opts.WillEnabled {
		pahoOpts.SetWill(opts.WillTopic, string(opts.WillPayload), opts.WillQos, opts.WillRetained)
	}

	return pahoOpts, nil
}

func setDefaultOptions(opts *mqtt.ClientOptions) {
	if len(opts.Servers) == 0 {
		defaultBroker := cmp.Or(os.Getenv("PIGO_MQTT_BROKER"), "tcp://127.0.0.1:1883")
		opts.AddBroker(defaultBroker)
	}

	if opts.Username == "" {
		opts.SetUsername(cmp.Or(os.Getenv("PIGO_MQTT_USERNAME"), ""))
	}
	if opts.Password == "" {
		opts.SetPassword(cmp.Or(os.Getenv("PIGO_MQTT_PASSWORD"), ""))
	}
	if opts.ClientID == "" {
		opts.SetClientID(fmt.Sprintf("pigo-logrepl-%s", rand.NewName()))
	}
}

func getBrokerStrings(opts *mqtt.ClientOptions) []string {
	brokers := make([]string, len(opts.Servers))
	for i, server := range opts.Servers {
		brokers[i] = server.String()
	}
	return brokers
}

func parseArgs(args []any) []any {
	var topicPrefix string
	if len(args) > 0 {
		if tp, ok := args[0].(string); ok {
			topicPrefix = tp
		}
	}

	// Use default if empty
	if topicPrefix == "" {
		topicPrefix = "/pigo"
	}

	// Ensure the prefix starts with "/"
	if !strings.HasPrefix(topicPrefix, "/") {
		topicPrefix = "/" + topicPrefix
	}

	return []any{topicPrefix}
}
