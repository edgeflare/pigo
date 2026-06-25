// Package logger wraps log/slog with pluggable output sinks (stderr, OTel, ...).
package logger

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Options configures Logger sinks and format.
type Options struct {
	Level          slog.Level
	AddSource      bool
	JSON           bool
	Stderr         bool
	OTel           bool
	OTelScope      string // default "pigo-<hostname>"
	LoggerProvider *sdklog.LoggerProvider
}

// New builds a *slog.Logger writing to all enabled sinks (or discarding if none).
func New(opts Options) (*slog.Logger, error) {
	var handlers []slog.Handler

	if opts.Stderr {
		ho := &slog.HandlerOptions{Level: opts.Level, AddSource: opts.AddSource}
		if opts.JSON {
			handlers = append(handlers, slog.NewJSONHandler(os.Stderr, ho))
		} else {
			handlers = append(handlers, slog.NewTextHandler(os.Stderr, ho))
		}
	}

	if opts.OTel {
		if opts.LoggerProvider == nil {
			return nil, fmt.Errorf("logger: OTel=true but LoggerProvider is nil")
		}
		scope := cmp.Or(opts.OTelScope, "pigo-"+func() string { h, _ := os.Hostname(); return h }())
		handlers = append(handlers, otelslog.NewHandler(scope, otelslog.WithLoggerProvider(opts.LoggerProvider)))
	}

	if len(handlers) == 0 {
		handlers = append(handlers, slog.NewJSONHandler(io.Discard, nil))
	}

	return slog.New(NewFanoutHandler(handlers...)), nil
}

// ParseLevel maps a flag string to slog.Level; unknown values yield Info.
func ParseLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
