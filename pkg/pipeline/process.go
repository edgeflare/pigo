package pipeline

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/edgeflare/pigo/pkg/metrics"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/edgeflare/pigo/pkg/pipeline/transform"
	"github.com/prometheus/client_golang/prometheus"
)

func distributeToSinks(
	pl Pipeline,
	source Source,
	event cdc.Event,
	sinkChannels map[string]chan cdc.Event,
) {
	for _, sink := range pl.Sinks {
		if ch, ok := sinkChannels[sink.Name]; ok {
			select {
			case ch <- event:
				metrics.ProcessedEvents.WithLabelValues(
					pl.Name,
					source.Name,
					sink.Name,
				).Inc()
			default:
				log.Printf("Warning: Sink channel %s is full", sink.Name)
			}
		}
	}
}

func applyTransformations(event *cdc.Event, transformations []transform.Transformation) (*cdc.Event, error) {
	if len(transformations) == 0 {
		return event, nil
	}

	if event == nil {
		return nil, fmt.Errorf("cannot transform nil event")
	}

	manager := transform.NewManager()
	manager.RegisterBuiltins()

	chainTransformations, err := manager.Chain(transformations)
	if err != nil {
		return nil, fmt.Errorf("error creating transformation pipeline: %w", err)
	}

	result, err := chainTransformations(event)
	if result == nil && err == nil {
		// Transform indicated event should be filtered out
		return nil, nil
	}
	return result, err
}

// processSinkEvents handles events from multiple sources
func processSinkEvents(
	ctx context.Context,
	wg *sync.WaitGroup,
	pl Pipeline,
	sink Sink,
	peer *Peer,
	ch <-chan cdc.Event,
) {
	defer wg.Done()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}

			// Apply sink-specific transformations
			transformedEvent, err := applyTransformations(&event, sink.Transformations)
			if err != nil {
				metrics.TransformationErrors.WithLabelValues(
					"sink",
					pl.Name,
					"multiple",
					sink.Name,
				).Inc()
				log.Printf("Sink transformation error: %v", err)
				continue
			}
			if transformedEvent == nil {
				continue
			}

			// Publish the transformed event
			if err := peer.Connector().Pub(*transformedEvent); err != nil {
				metrics.PublishErrors.WithLabelValues(sink.Name).Inc()
				log.Printf("Publish error to %s: %v", peer.Name, err)
			}

		case <-ctx.Done():
			return
		}
	}
}

// processEvent handles the processing of a single event
func ProcessEvent(
	pl Pipeline,
	source Source,
	event cdc.Event,
	sinkChannels map[string]chan cdc.Event,
) {
	timer := prometheus.NewTimer(metrics.EventProcessingDuration.WithLabelValues(
		pl.Name,
		source.Name,
		"",
	))
	defer timer.ObserveDuration()

	// Process source transformations
	transformedEvent := applyEventTransformations(event, source, pl, sinkChannels)
	if transformedEvent == nil {
		return
	}

	// Distribute to sinks
	distributeToSinks(pl, source, *transformedEvent, sinkChannels)
}

// applyEventTransformations applies all transformations to an event
func applyEventTransformations(
	event cdc.Event,
	source Source,
	pl Pipeline,
	sinkChannels map[string]chan cdc.Event,
) *cdc.Event {
	// Source transformations
	transformed, err := applyTransformations(&event, source.Transformations)
	if err != nil {
		metrics.TransformationErrors.WithLabelValues(
			"source",
			pl.Name,
			source.Name,
			"",
		).Inc()
		log.Printf("Source transformation error: %v", err)
		return nil
	}
	if transformed == nil {
		return nil
	}

	// Pipeline transformations
	transformed, err = applyTransformations(transformed, pl.Transformations)
	if err != nil {
		metrics.TransformationErrors.WithLabelValues(
			"pipeline",
			pl.Name,
			source.Name,
			"",
		).Inc()
		log.Printf("Pipeline transformation error: %v", err)
		return nil
	}

	return transformed
}
