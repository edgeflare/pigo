package logger

import (
	"context"
	"errors"
	"log/slog"
)

// fanoutHandler fans records out to multiple handlers, like io.MultiWriter.
type fanoutHandler struct {
	handlers []slog.Handler
}

// NewFanoutHandler returns a Handler delegating to each of hs.
func NewFanoutHandler(handlers ...slog.Handler) slog.Handler {
	return &fanoutHandler{handlers: handlers}
}

func (m *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		nh[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: nh}
}

func (m *fanoutHandler) WithGroup(name string) slog.Handler {
	nh := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		nh[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: nh}
}
