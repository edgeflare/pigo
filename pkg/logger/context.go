package logger

import (
	"context"
	"log/slog"
)

// contextKey is used as a pointer for context.WithValue.
type contextKey struct{ name string }

func (k *contextKey) String() string { return "logger context value " + k.name }

var loggerKey = &contextKey{"logger"}

// WithContext returns a copy of ctx carrying l.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext returns the Logger stored in ctx, or slog.Default() if none.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
