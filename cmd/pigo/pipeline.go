package pigo

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/edgeflare/pigo/pkg/metrics"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	// Register built-in connectors
	_ "github.com/edgeflare/pigo/pkg/pipeline/peer/clickhouse"
	_ "github.com/edgeflare/pigo/pkg/pipeline/peer/debug"
	_ "github.com/edgeflare/pigo/pkg/pipeline/peer/grpc"
	_ "github.com/edgeflare/pigo/pkg/pipeline/peer/kafka"
	"github.com/edgeflare/pigo/pkg/pipeline/peer/mqtt"
	"github.com/edgeflare/pigo/pkg/pipeline/peer/nats"

	"github.com/edgeflare/pigo/pkg/pipeline/peer/pg"
)

var (
	prometheusEnabled bool
	prometheusAddr    string
)

var pipelineCmd = &cobra.Command{
	Use:     "pipeline",
	Aliases: []string{"p"},
	Short:   "Run the PIGO pipeline",
	Long:    `Run the PIGO pipeline to replicate data changes from PostgreSQL to various destinations.`,
	RunE:    runPipeline,
}

func runPipeline(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	doneChan := make(chan struct{})

	var wg sync.WaitGroup

	if prometheusEnabled {
		go metrics.StartPrometheusServer(ctx, &wg, &metrics.PromServerOpts{Addr: prometheusAddr})
	}

	m := pipeline.NewManager()

	if err := m.Init(&cfg.Pipeline); err != nil {
		return fmt.Errorf("failed to initialize peers: %w", err)
	}

	if err := startPipelineProcessing(ctx, m, &wg, errChan); err != nil {
		return fmt.Errorf("failed to start pipeline processing: %w", err)
	}

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		log.Println("Received termination signal, shutting down gracefully...")
		cancel()
	case err := <-errChan:
		log.Printf("Pipeline error: %v", err)
		cancel()
	}

	// Wait for goroutines to complete
	go func() {
		wg.Wait()
		close(doneChan)
	}()

	// Wait with timeout
	select {
	case <-doneChan:
		log.Println("Shutdown complete")
	case <-time.After(10 * time.Second):
		log.Println("Shutdown timed out after 10 seconds")
	}

	return nil
}

func startPipelineProcessing(
	ctx context.Context,
	m *pipeline.Manager,
	wg *sync.WaitGroup,
	errChan chan<- error,
) error {
	for _, pl := range cfg.Pipeline.Pipelines {
		if err := setupPipeline(ctx, m, wg, pl); err != nil {
			return fmt.Errorf("failed to setup pipeline %s: %w", pl.Name, err)
		}
	}
	return nil
}

// setupSource configures and starts a single source within a pipeline
func setupSource(
	ctx context.Context,
	m *pipeline.Manager,
	wg *sync.WaitGroup,
	pl pipeline.Pipeline,
	source pipeline.Source,
	sinkChannels map[string]chan cdc.Event,
) error {
	sourcePeer := cfg.Pipeline.GetPeer(source.Name)
	if sourcePeer == nil {
		return fmt.Errorf("source peer %s not found", source.Name)
	}

	peer, err := m.GetPeer(source.Name)
	if err != nil {
		return err
	}

	// Check if this is the first subscription before adding the new one
	isFirst := m.IsFirstSubscription(source.Name)

	// Add the subscription
	m.AddSubscription(source.Name, pl.Name, sinkChannels)

	// Only set up the source connection for the first subscription
	if isFirst {
		eventsChan, err := setupSourceConnection(sourcePeer, peer)
		if err != nil {
			return err
		}

		// Start source event processing with fan-out
		wg.Add(1)
		go processSourceEventsWithFanout(ctx, wg, m, source.Name, eventsChan)
	}

	// Setup sinks for this pipeline
	if err := pipeline.SetupSinks(ctx, m, wg, pl, sinkChannels); err != nil {
		return fmt.Errorf("failed to setup sinks: %w", err)
	}

	return nil
}

// setupSourceConnection establishes the connection for a source based on its type
func setupSourceConnection(sourcePeer *pipeline.Peer, peer *pipeline.Peer) (<-chan cdc.Event, error) {
	switch sourcePeer.ConnectorName {
	case "postgres":
		var cfg pg.Config
		if err := unmarshalConfig(sourcePeer.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error parsing postgres config: %w", err)
		}
		return peer.Connector().Sub()
	case "mqtt":
		var cfg mqtt.Config
		if err := unmarshalConfig(sourcePeer.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error parsing MQTT config: %w", err)
		}
		return peer.Connector().Sub(cfg.TopicPrefix)
	case "grpc":
		return setupGRPCConnection(sourcePeer, peer)
	case "nats":
		var cfg nats.Config
		if err := unmarshalConfig(sourcePeer.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error parsing NATS config: %w", err)
		}
		return peer.Connector().Sub()
	default:
		return nil, fmt.Errorf("unsupported source connector: %s", sourcePeer.ConnectorName)
	}
}

// setupGRPCConnection handles gRPC-specific connection setup
func setupGRPCConnection(sourcePeer *pipeline.Peer, peer *pipeline.Peer) (<-chan cdc.Event, error) {
	var cfg struct {
		Address  string `json:"address"`
		IsServer bool   `json:"isServer"`
	}

	if err := unmarshalConfig(sourcePeer.Config, &cfg); err != nil {
		return nil, fmt.Errorf("error parsing grpc config: %w", err)
	}

	if cfg.IsServer {
		return nil, fmt.Errorf("cannot subscribe to gRPC server peer")
	}

	return peer.Connector().Sub()
}

// unmarshalConfig is a helper function to handle config unmarshaling
func unmarshalConfig(config interface{}, target interface{}) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshaling config: %w", err)
	}

	if err := json.Unmarshal(jsonData, target); err != nil {
		return fmt.Errorf("error unmarshaling config: %w", err)
	}

	return nil
}

func processSourceEventsWithFanout(
	ctx context.Context,
	wg *sync.WaitGroup,
	m *pipeline.Manager,
	sourceName string,
	eventsChan <-chan cdc.Event,
) {
	defer wg.Done()

	for {
		select {
		case event, ok := <-eventsChan:
			if !ok {
				return
			}

			// Get all subscriptions for this source
			subs := m.GetSubscriptions(sourceName)

			// Fan out the event to all subscribed pipelines
			for _, sub := range subs {
				// Get pipeline config for this subscription
				pl := cfg.Pipeline.GetPipeline(sub.PipelineName)
				if pl == nil {
					log.Printf("Pipeline %s not found", sub.PipelineName)
					continue
				}

				// Find the matching source config for this event's source
				var matchingSource *pipeline.Source
				for _, src := range pl.Sources {
					if src.Name == sourceName {
						matchingSource = &src
						break
					}
				}

				if matchingSource == nil {
					log.Printf("Source %s not found in pipeline %s", sourceName, sub.PipelineName)
					continue
				}

				// Process the event with this pipeline's configuration using the matched source
				pipeline.ProcessEvent(*pl, *matchingSource, event, sub.SinkChannels)
			}

		case <-ctx.Done():
			return
		}
	}
}

// setupPipeline handles the setup of a single pipeline
func setupPipeline(ctx context.Context, m *pipeline.Manager, wg *sync.WaitGroup, pl pipeline.Pipeline) error {
	// Create channels for each sink that will be shared across all sources
	sinkChannels := make(map[string]chan cdc.Event)
	for _, sink := range pl.Sinks {
		sinkChannels[sink.Name] = make(chan cdc.Event, 100)
	}

	// Setup each source independently
	for _, source := range pl.Sources {
		if err := setupSource(ctx, m, wg, pl, source, sinkChannels); err != nil {
			// Close all sink channels on error
			for _, ch := range sinkChannels {
				close(ch)
			}
			return fmt.Errorf("failed to setup source %s: %w", source.Name, err)
		}
	}

	// Setup sinks to process events from all sources
	return pipeline.SetupSinks(ctx, m, wg, pl, sinkChannels)
}

func init() {
	pipelineCmd.Flags().BoolVar(&prometheusEnabled, "metrics", true, "Enable Prometheus metrics server")
	pipelineCmd.Flags().StringVar(&prometheusAddr, "metrics-addr", ":9100", "Prometheus metrics server address")

	err := viper.BindPFlag("metrics.enabled", pipelineCmd.Flags().Lookup("metrics"))
	if err != nil {
		log.Fatalf("Error binding flag 'metrics.enabled': %v", err)
	}

	err = viper.BindPFlag("metrics.addr", pipelineCmd.Flags().Lookup("metrics-addr"))
	if err != nil {
		log.Fatalf("Error binding flag 'metrics.addr': %v", err)
	}
}
