package pipeline

import (
	"encoding/json"
	"errors"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

type ConnectorType int

const (
	ConnectorTypeUnknown ConnectorType = iota
	ConnectorTypePub                   // Sink / consumer-only
	ConnectorTypeSub                   // Source / producer-only
	ConnectorTypePubSub                // Source and sink
)

var (
	ErrConnectorTypeMismatch = errors.New("connector type mismatch")
)

// A Connector represents a data pipeline component.
type Connector interface {
	// Connect initializes the connector with the provided configuration.
	// The config parameter is a raw JSON message containing connector-specific settings.
	// Additional arguments can be passed via the args parameter.
	Connect(config json.RawMessage, args ...any) error

	// Pub sends the given CDC event to the connector's destination.
	// It returns an error if the publish operation fails.
	Pub(event cdc.Event, args ...any) error

	// Sub provides a channel for consumingCDC events.
	Sub(args ...any) (<-chan cdc.Event, error)

	// Type returns the type of the connector (SUB, PUB, or PUBSUB)
	Type() ConnectorType

	Disconnect() error
}

// Predefined connectors
const (
	ConnectorClickHouse = "clickhouse"
	ConnectorDebug      = "debug"
	ConnectorHTTP       = "http"
	ConnectorKafka      = "kafka"
	ConnectorMQTT       = "mqtt"
	ConnectorNATS       = "nats"
	ConnectorGRPC       = "grpc"
	ConnectorPostgres   = "postgres"
)

// RegisterConnector adds a new connector to the registry.
// The name parameter is used as a key to identify the connector type.
func RegisterConnector(name string, c Connector) {
	connectors[name] = c
}
