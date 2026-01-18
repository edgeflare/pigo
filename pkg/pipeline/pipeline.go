package pipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/edgeflare/pigo/pkg/pipeline/transform"
)

// Sink is a pipeline output with its transformations.
type Sink struct {
	// Name must match one of configured peers
	Name string `mapstructure:"name"`
	// Sink-specific transformations are applied after source transformations, pipeline transformations and before sending to speceific sink
	Transformations []transform.Transformation `mapstructure:"transformations"`
}

// Pipeline configures a complete data processing pipeline.
type Pipeline struct {
	Name    string   `mapstructure:"name"`
	Sources []Source `mapstructure:"sources"`
	// Pipeline transformations are applied after source transformations and before sink transformations.
	// These are applied to all CDC events flowing through a pipeline from its all sources to all sinks
	Transformations []transform.Transformation `mapstructure:"transformations"`
	Sinks           []Sink                     `mapstructure:"sinks"`
}

type Config struct {
	Peers     []Peer     `mapstructure:"peers"`
	Pipelines []Pipeline `mapstructure:"pipelines"`
}

func (c *Config) GetPeer(peerName string) *Peer {
	for _, peer := range c.Peers {
		if peer.Name == peerName {
			return &peer
		}
	}
	return nil
}

func (c *Config) GetPipeline(pipelineName string) *Pipeline {
	for _, pipeline := range c.Pipelines {
		if pipeline.Name == pipelineName {
			return &pipeline
		}
	}
	return nil
}

func SetupSinks(
	ctx context.Context,
	m *Manager,
	wg *sync.WaitGroup,
	pl Pipeline,
	sinkChannels map[string]chan cdc.Event,
) error {
	for _, sink := range pl.Sinks {
		sinkPeer, err := m.GetPeer(sink.Name)
		if err != nil {
			return fmt.Errorf("sink peer %s not found: %w", sink.Name, err)
		}

		ch := sinkChannels[sink.Name]
		wg.Add(1)
		go processSinkEvents(ctx, wg, pl, sink, sinkPeer, ch)
	}
	return nil
}
