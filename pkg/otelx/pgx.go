package otelx

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

const pgxScope = "github.com/edgeflare/pigo/pkg/pgutil"

type ctxKey struct{}

type queryStart struct {
	t    time.Time
	span trace.Span
}

// PgxTracer implements pgx.QueryTracer using the Provider's tracer and meter.
type PgxTracer struct {
	tracer    trace.Tracer
	meter     metric.Meter
	histogram metric.Float64Histogram
	initOnce  sync.Once
}

// NewPgxTracer returns a [pgx.QueryTracer] backed by p.
func NewPgxTracer(p *Provider) (*PgxTracer, error) {
	m := p.Meter(pgxScope)
	histogram, err := m.Float64Histogram(
		"db.client.operation.duration", // semconv name
		metric.WithUnit("s"),
		metric.WithDescription("Duration of database client operations"),
		metric.WithExplicitBucketBoundaries(.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5),
	)
	if err != nil {
		return nil, err
	}
	return &PgxTracer{
		tracer:    p.Tracer(pgxScope),
		meter:     m,
		histogram: histogram,
	}, nil
}

// RegisterPoolStats registers async gauge instruments that scrape pgxpool.Stat
// on every metrics export. Call once after the pool is dialled.
func (t *PgxTracer) RegisterPoolStats(pool *pgxpool.Pool) error {
	var err error
	t.initOnce.Do(func() {
		_, err = t.meter.Int64ObservableGauge(
			"db.client.connection.count",
			metric.WithDescription("Number of connections in the pool by state"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				s := pool.Stat()
				o.Observe(int64(s.IdleConns()), metric.WithAttributes(attribute.String("state", "idle")))
				o.Observe(int64(s.AcquiredConns()), metric.WithAttributes(attribute.String("state", "used")))
				o.Observe(int64(s.TotalConns()), metric.WithAttributes(attribute.String("state", "total")))
				return nil
			}),
		)
	})
	return err
}

func (t *PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	op := sqlOperation(data.SQL)
	ctx, span := t.tracer.Start(ctx, "pg."+strings.ToLower(op),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			semconv.DBQueryText(data.SQL),
			semconv.DBOperationName(op),
		),
	)
	return context.WithValue(ctx, ctxKey{}, queryStart{t: time.Now(), span: span})
}

func (t *PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	v, ok := ctx.Value(ctxKey{}).(queryStart)
	if !ok {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("db.system.name", "postgresql"),
		semconv.DBOperationName(data.CommandTag.String()),
		attribute.Bool("error", data.Err != nil),
	}

	t.histogram.Record(ctx, time.Since(v.t).Seconds(), metric.WithAttributes(attrs...))

	if data.Err != nil {
		v.span.RecordError(data.Err)
		v.span.SetStatus(codes.Error, data.Err.Error())
	} else {
		v.span.SetAttributes(attribute.Int64("db.rows_affected", data.CommandTag.RowsAffected()))
	}
	v.span.End()
}

// sqlOperation extracts the leading SQL verb (SELECT, INSERT, …) for use as
// span name and db.operation.name attribute.
func sqlOperation(sql string) string {
	sql = strings.TrimSpace(sql)
	if i := strings.IndexByte(sql, ' '); i > 0 {
		return strings.ToUpper(sql[:i])
	}
	if len(sql) > 16 {
		return strings.ToUpper(sql[:16])
	}
	return strings.ToUpper(sql)
}
