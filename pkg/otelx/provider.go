// Package otelx initialises OpenTelemetry traces and metrics over OTLP/gRPC.
//
//	p, _ := otelx.New(ctx, otelx.Options{ServiceName: "svc", Traces: true, Metrics: true, Insecure: true})
//	defer p.Shutdown(ctx)
package otelx

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// Options configures the OTel provider.
type Options struct {
	// ServiceName is the logical name of the service, e.g. "pigo".
	// Defaults to "unknown_service" if empty.
	ServiceName string

	// ServiceVersion is the semver string of the service, e.g. "1.2.3".
	ServiceVersion string

	// Traces enables the OTLP gRPC trace exporter.
	Traces bool

	// Metrics enables the OTLP gRPC metrics exporter.
	Metrics bool

	// Insecure disables TLS for the OTLP gRPC connection (useful in dev).
	Insecure bool

	// OTLPEndpoint overrides the OTLP collector endpoint.
	// Defaults to "localhost:4317" when Insecure is true, or the SDK default otherwise.
	// The OTEL_EXPORTER_OTLP_ENDPOINT env var takes precedence over this field.
	OTLPEndpoint string

	// TracerProvider allows injecting a pre-built provider (e.g. for testing).
	// When set, Traces, Metrics and exporter options are ignored.
	TracerProvider trace.TracerProvider

	// MeterProvider allows injecting a pre-built provider (e.g. for testing).
	MeterProvider metric.MeterProvider
}

// Provider wraps the SDK trace and meter providers and exposes helpers
// for creating named tracers and meters. It is safe for concurrent use.
type Provider struct {
	opts     Options
	tracer   trace.TracerProvider
	meter    metric.MeterProvider
	sdkTP    *sdktrace.TracerProvider
	sdkMP    *sdkmetric.MeterProvider
	resource *resource.Resource // stored during New()
}

// New initialises a Provider according to opts.
// Call Shutdown when the process exits to flush pending telemetry.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.ServiceName == "" {
		opts.ServiceName = "unknown_service"
	}

	p := &Provider{opts: opts}

	// If callers injected their own providers, use them directly.
	if opts.TracerProvider != nil || opts.MeterProvider != nil {
		p.tracer = opts.TracerProvider
		p.meter = opts.MeterProvider
		if p.tracer == nil {
			p.tracer = otel.GetTracerProvider()
		}
		if p.meter == nil {
			p.meter = otel.GetMeterProvider()
		}
		return p, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(opts.ServiceVersion),
		),
		resource.WithProcessPID(),
		resource.WithHost(),
	)
	if err != nil {
		// Non-fatal: resource detection can partially fail (e.g. no host metadata).
		res = resource.Default()
	}
	p.resource = res

	if opts.Traces {
		tp, err := newTracerProvider(ctx, opts, res)
		if err != nil {
			return nil, fmt.Errorf("otel: trace provider: %w", err)
		}
		p.sdkTP = tp
		p.tracer = tp
		// Register globally so libraries that call otel.Tracer() are captured.
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	} else {
		p.tracer = otel.GetTracerProvider() // no-op by default
	}

	if opts.Metrics {
		mp, err := newMeterProvider(ctx, opts, res)
		if err != nil {
			_ = p.Shutdown(ctx) // clean up the trace provider we just built
			return nil, fmt.Errorf("otel: meter provider: %w", err)
		}
		p.sdkMP = mp
		p.meter = mp
		otel.SetMeterProvider(mp)
	} else {
		p.meter = otel.GetMeterProvider() // no-op by default
	}

	return p, nil
}

func (p *Provider) Propagator() propagation.TextMapPropagator {
	return otel.GetTextMapPropagator()
}

func (p *Provider) Resource() *resource.Resource {
	if p.resource != nil {
		return p.resource
	}
	return resource.Default()
}

func (p *Provider) ForceFlush(ctx context.Context) error {
	var errs []error
	if p.sdkTP != nil {
		if err := p.sdkTP.ForceFlush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if p.sdkMP != nil {
		if err := p.sdkMP.ForceFlush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("otel: force flush: %v", errs)
	}
	return nil
}

// Tracer returns a named [trace.Tracer]. scope is typically the import path
// of the package that owns the instrumentation, e.g. "github.com/edgeflare/pigo/pkg/pg".
func (p *Provider) Tracer(scope string, opts ...trace.TracerOption) trace.Tracer {
	return p.tracer.Tracer(scope, opts...)
}

// Meter returns a named [metric.Meter]. scope follows the same convention as Tracer.
func (p *Provider) Meter(scope string, opts ...metric.MeterOption) metric.Meter {
	return p.meter.Meter(scope, opts...)
}

// TracerProvider returns the underlying [trace.TracerProvider].
func (p *Provider) TracerProvider() trace.TracerProvider { return p.tracer }

// MeterProvider returns the underlying [metric.MeterProvider].
func (p *Provider) MeterProvider() metric.MeterProvider { return p.meter }

// Shutdown flushes and stops all exporters. It should be called once,
// typically via defer, before the process exits.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error
	if p.sdkTP != nil {
		if err := p.sdkTP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace provider shutdown: %w", err))
		}
	}
	if p.sdkMP != nil {
		if err := p.sdkMP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("otel: shutdown: %v", errs)
	}
	return nil
}

func newTracerProvider(ctx context.Context, opts Options, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	expOpts := []otlptracegrpc.Option{}
	if opts.Insecure {
		expOpts = append(expOpts, otlptracegrpc.WithInsecure())
	}
	if opts.OTLPEndpoint != "" {
		expOpts = append(expOpts, otlptracegrpc.WithEndpoint(opts.OTLPEndpoint))
	}

	exp, err := otlptracegrpc.New(ctx, expOpts...)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exp)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	), nil
}

func newMeterProvider(ctx context.Context, opts Options, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	expOpts := []otlpmetricgrpc.Option{}
	if opts.Insecure {
		expOpts = append(expOpts, otlpmetricgrpc.WithInsecure())
	}
	if opts.OTLPEndpoint != "" {
		expOpts = append(expOpts, otlpmetricgrpc.WithEndpoint(opts.OTLPEndpoint))
	}

	exp, err := otlpmetricgrpc.New(ctx, expOpts...)
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
		sdkmetric.WithResource(res),
	), nil
}
