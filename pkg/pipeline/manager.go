package pipeline

import (
	"encoding/json"
	"fmt"
	"plugin"
	"sync"
	"time"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/edgeflare/pigo/pkg/pipeline/transform"
	"go.uber.org/zap"
)

var (
	connectors    = make(map[string]Connector)
	peers         = make(map[string]Peer)
	subscriptions = make(map[string][]SourceSubscription)
	mu            sync.RWMutex
)

type SourceSubscription struct {
	SinkChannels map[string]chan cdc.Event
	PipelineName string
}

// Manager handles connectors and peers for data pipeline operations.
type Manager struct {
	connectors    map[string]Connector
	peers         map[string]Peer
	subscriptions map[string][]SourceSubscription
	logger        *zap.Logger
}

// NewManager returns a new Manager instance with the default connectors.
func NewManager() *Manager {
	logger, _ := zap.NewProduction()

	return &Manager{
		connectors:    connectors,
		peers:         map[string]Peer{},
		subscriptions: map[string][]SourceSubscription{},
		logger:        logger,
	}
}

// RegisterConnectorPlugin loads and registers a connector plugin from the specified path.
func (m *Manager) RegisterConnectorPlugin(path string, name string) error {
	plug, err := plugin.Open(path)
	if err != nil {
		return err
	}

	symbol, err := plug.Lookup("Connector")
	if err != nil {
		return err
	}

	connector, ok := symbol.(*Connector)
	if !ok {
		return fmt.Errorf("invalid connector plugin")
	}

	RegisterConnector(name, *connector)
	return nil
}

// NewPeer creates a new Peer
func (m *Manager) AddPeer(connector string, name string) (*Peer, error) {
	if _, exists := m.connectors[connector]; !exists {
		return nil, fmt.Errorf("connector %s not found", connector)
	}

	peer := Peer{ConnectorName: connector, Name: name}
	m.peers[name] = peer
	return &peer, nil
}

func (m *Manager) Peers() []Peer {
	peers := make([]Peer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	return peers
}

func (m *Manager) GetPeer(name string) (*Peer, error) {
	if peer, exists := m.peers[name]; exists {
		return &peer, nil
	}
	return nil, fmt.Errorf("peer %s not found", name)
}

// AddSubscription adds a new subscription for a source
func (m *Manager) AddSubscription(sourceName, pipelineName string, sinkChannels map[string]chan cdc.Event) {
	mu.Lock()
	m.subscriptions[sourceName] = append(m.subscriptions[sourceName], SourceSubscription{
		PipelineName: pipelineName,
		SinkChannels: sinkChannels,
	})
	mu.Unlock()
}

// GetSubscriptions returns all subscriptions for a source
func (m *Manager) GetSubscriptions(sourceName string) []SourceSubscription {
	mu.RLock()
	defer mu.RUnlock()
	return m.subscriptions[sourceName]
}

// IsFirstSubscription checks if this is the first subscription for a source
func (m *Manager) IsFirstSubscription(sourceName string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return len(m.subscriptions[sourceName]) == 0
}

// Source is a pipeline input with its transformations.
type Source struct {
	// Name must match one of configured peers
	Name string `mapstructure:"name"`
	// Source transformations are applied (in the order specified) as soon as CDC event is received before any processing.
	Transformations []transform.Transformation `mapstructure:"transformations"`
}

// Init initializes all peers from configuration
func (m *Manager) Init(config *Config) error {
	m.logger.Info("Initializing pipeline manager", zap.Int("peerCount", len(config.Peers)))
	for _, p := range config.Peers {
		m.logger.Debug("Adding peer",
			zap.String("name", p.Name),
			zap.String("connector", p.ConnectorName))

		peer, err := m.AddPeer(p.ConnectorName, p.Name)
		if err != nil {
			m.logger.Error("Failed to add peer",
				zap.String("name", p.Name),
				zap.String("connector", p.ConnectorName),
				zap.Error(err))
			return fmt.Errorf("failed to add peer %s: %w", p.Name, err)
		}

		// Store the config in the peer
		peer.Config = p.Config
		configJSON, err := json.Marshal(peer.Config)
		if err != nil {
			m.logger.Error("Failed to marshal config for peer",
				zap.String("name", peer.Name),
				zap.Error(err))
			return fmt.Errorf("failed to marshal config for peer %s: %w", peer.Name, err)
		}

		// Initialize each peer with its configuration
		connector := peer.Connector()
		if connector == nil {
			m.logger.Error("Connector not found for peer",
				zap.String("name", peer.Name))
			return fmt.Errorf("connector not found for peer %s", peer.Name)
		}

		m.logger.Debug("Connecting peer",
			zap.String("name", peer.Name),
			zap.String("connector", p.ConnectorName))

		if err := connector.Connect(json.RawMessage(configJSON)); err != nil {
			// Retry logic with simple backoff
			delays := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
			var success bool

			for _, delay := range delays {
				m.logger.Warn("Retrying connection",
					zap.String("name", peer.Name),
					zap.Duration("delay", delay))
				time.Sleep(delay)

				if err = connector.Connect(json.RawMessage(configJSON)); err == nil {
					success = true
					break
				}
			}

			if !success {
				m.logger.Error("Failed to initialize connector after retries",
					zap.String("name", peer.Name),
					zap.Error(err))
				return fmt.Errorf("failed to initialize connector %s: %w", peer.Name, err)
			}
		}

		m.logger.Info("Successfully connected peer",
			zap.String("name", peer.Name),
			zap.String("connector", p.ConnectorName))
	}

	m.logger.Info("Successfully initialized all peers", zap.Int("totalPeers", len(m.peers)))
	return nil
}
