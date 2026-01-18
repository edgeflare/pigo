package kafka

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/IBM/sarama"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

// PeerKafka implements the source and sink for Kafka
type PeerKafka struct {
	producer    sarama.SyncProducer
	config      *Config
	topicPrefix string
}

func (p *PeerKafka) Connect(config json.RawMessage, args ...any) error {
	var cfg Config
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal Kafka config: %w", err)
	}

	// Set defaults if not provided
	if len(cfg.Brokers) == 0 {
		cfg.Brokers = []string{"localhost:9092"}
	}
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "pigo"
	}
	if cfg.Version == "" {
		cfg.Version = "2.1.1"
	}
	if cfg.Partitions == 0 {
		cfg.Partitions = 1
	}
	if cfg.Replicas == 0 {
		cfg.Replicas = 1
	}
	if cfg.RetentionMS == 0 {
		cfg.RetentionMS = 7 * 24 * 60 * 60 * 1000 // 7 days
	}

	saramaConfig := sarama.NewConfig()

	version, err := sarama.ParseKafkaVersion(cfg.Version)
	if err != nil {
		return fmt.Errorf("invalid Kafka version: %w", err)
	}
	saramaConfig.Version = version

	// Configure producer
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Retry.Max = 5
	saramaConfig.Producer.Retry.Backoff = time.Second
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.Return.Errors = true

	// Configure SASL if enabled
	if cfg.SASL != nil && cfg.SASL.Enable {
		saramaConfig.Net.SASL.Enable = true
		saramaConfig.Net.SASL.User = cfg.SASL.Username
		saramaConfig.Net.SASL.Password = cfg.SASL.Password

		switch cfg.SASL.Algorithm {
		case "sha256":
			saramaConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		case "sha512":
			saramaConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		default:
			saramaConfig.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		}
	}

	// Create producer
	producer, err := sarama.NewSyncProducer(cfg.Brokers, saramaConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kafka producer: %w", err)
	}

	p.producer = producer
	p.config = &cfg
	p.topicPrefix = cfg.TopicPrefix

	// Create admin client for topic management
	admin, err := sarama.NewClusterAdmin(cfg.Brokers, saramaConfig)
	if err != nil {
		producer.Close()
		return fmt.Errorf("failed to create cluster admin: %w", err)
	}
	defer admin.Close()

	// Ensure default topic exists
	err = p.ensureDefaultTopic(admin)
	if err != nil {
		producer.Close()
		return fmt.Errorf("failed to ensure default topic: %w", err)
	}

	return nil
}

func (p *PeerKafka) Pub(event cdc.Event, args ...any) error {
	if p.producer == nil {
		return fmt.Errorf("Kafka producer not initialized")
	}

	// create topic based on prefix, schema, table and operation
	topic := fmt.Sprintf("%s.%s.%s.%s",
		p.topicPrefix,
		event.Payload.Source.Schema,
		event.Payload.Source.Table,
		event.Payload.Op)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal CDC event: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.ByteEncoder(data),
	}

	// TODO: add key if primary key is available

	partition, offset, err := p.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	log.Printf("Published message to topic %s [partition: %d, offset: %d]",
		topic, partition, offset)

	return nil
}

func (p *PeerKafka) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePub
}

func (p *PeerKafka) Disconnect() error {
	if p.producer != nil {
		return p.producer.Close()
	}
	return nil
}

func (p *PeerKafka) ensureDefaultTopic(admin sarama.ClusterAdmin) error {
	defaultTopic := fmt.Sprintf("%s.pg", p.topicPrefix)

	// check if topic exists
	topics, err := admin.ListTopics()
	if err != nil {
		return fmt.Errorf("failed to list topics: %w", err)
	}

	if _, exists := topics[defaultTopic]; !exists {
		// create topic configuration
		topicDetail := &sarama.TopicDetail{
			NumPartitions:     p.config.Partitions,
			ReplicationFactor: p.config.Replicas,
			ConfigEntries: map[string]*string{
				"retention.ms": stringPtr(fmt.Sprintf("%d", p.config.RetentionMS)),
			},
		}

		// Create topic
		err = admin.CreateTopic(defaultTopic, topicDetail, false)
		if err != nil {
			return fmt.Errorf("failed to create default topic: %w", err)
		}

		log.Printf("Created default topic: %s", defaultTopic)
	}

	return nil
}

func stringPtr(s string) *string {
	return &s
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorKafka, &PeerKafka{})
}

func (p *PeerKafka) Sub(args ...any) (<-chan cdc.Event, error) {
	// TODO: Implement
	return nil, pipeline.ErrConnectorTypeMismatch
}
