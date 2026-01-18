package metrics

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	TransformationErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pigo_transformation_errors_total",
			Help: "Total number of transformation errors by type and pipeline",
		},
		[]string{"error_type", "pipeline", "source", "sink"},
	)

	PublishErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pigo_publish_errors_total",
			Help: "Total number of publish errors by sink",
		},
		[]string{"sink"},
	)

	ProcessedEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pigo_processed_events_total",
			Help: "Total number of processed events by pipeline",
		},
		[]string{"pipeline", "source", "sink"},
	)

	EventProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pigo_event_processing_duration_seconds",
			Help:    "Duration of event processing",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"pipeline", "source", "sink"},
	)
)

type PromServerOpts struct {
	Addr              string
	Path              string        // Path for metrics endpoint, defaults to "/metrics"
	ShutdownTimeout   time.Duration // Timeout for server shutdown, defaults to 5 seconds
	ReadHeaderTimeout time.Duration // Timeout for reading request headers, defaults to 3 seconds
}

func defaultPrometheusServerOptions() PromServerOpts {
	return PromServerOpts{
		Addr:              ":9100",
		Path:              "/metrics",
		ShutdownTimeout:   5 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
	}
}

// StartPrometheusServer starts a Prometheus metrics server with the given options
// The server gracefully shutdown when the provided context is canceled
func StartPrometheusServer(ctx context.Context, wg *sync.WaitGroup, opts *PromServerOpts) {
	// merge with defaults
	effectiveOpts := defaultPrometheusServerOptions()
	if opts != nil {
		effectiveOpts.Addr = cmp.Or(opts.Addr, effectiveOpts.Addr)
		effectiveOpts.Path = cmp.Or(opts.Path, effectiveOpts.Path)
		effectiveOpts.ShutdownTimeout = cmp.Or(opts.ShutdownTimeout, effectiveOpts.ShutdownTimeout)
		effectiveOpts.ReadHeaderTimeout = cmp.Or(opts.ReadHeaderTimeout, effectiveOpts.ReadHeaderTimeout)
	}

	mux := http.NewServeMux()
	mux.Handle(effectiveOpts.Path, promhttp.Handler())
	server := &http.Server{
		Addr:              effectiveOpts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: effectiveOpts.ReadHeaderTimeout,
	}

	serverClosed := make(chan struct{})

	// Increment wait group
	wg.Add(1)

	// Start server
	go func() {
		defer wg.Done()
		log.Printf("Starting Prometheus metrics server on %s", effectiveOpts.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Metrics server error: %v", err)
		}
		close(serverClosed)
	}()

	// Monitor context cancellation in a separate goroutine
	go func() {
		<-ctx.Done()

		// Create a timeout context for shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), effectiveOpts.ShutdownTimeout)
		defer shutdownCancel()

		// Attempt graceful shutdown
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down metrics server: %v", err)
		}

		// Wait for server to close or timeout
		select {
		case <-serverClosed:
			log.Println("Metrics server shutdown complete")
		case <-shutdownCtx.Done():
			log.Println("Metrics server shutdown timed out")
		}
	}()
}
