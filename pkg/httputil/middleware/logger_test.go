package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observedRecord captures a single log record for assertion.
type observedRecord struct {
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

// observingHandler is a slog.Handler that collects records in memory.
type observingHandler struct {
	mu      sync.Mutex
	records []observedRecord
	level   slog.Level
	attrs   []slog.Attr
}

func (h *observingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *observingHandler) Handle(_ context.Context, r slog.Record) error {
	m := make(map[string]any)
	for _, a := range h.attrs {
		m[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, observedRecord{Level: r.Level, Message: r.Message, Attrs: m})
	h.mu.Unlock()
	return nil
}

func (h *observingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(merged, h.attrs)
	copy(merged[len(h.attrs):], attrs)
	return &observingHandler{level: h.level, attrs: merged}
}

func (h *observingHandler) WithGroup(name string) slog.Handler { return h }

func newTestLogger(level slog.Level) (*slog.Logger, *observingHandler) {
	h := &observingHandler{level: level}
	return slog.New(h), h
}

func TestLoggerWithOptions(t *testing.T) {
	logger, obs := newTestLogger(slog.LevelInfo)
	options := &LoggerOptions{
		Logger: logger,
		Format: func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []slog.Attr {
			return []slog.Attr{slog.String("test", "log")}
		},
	}

	handler := LoggerWithOptions(options)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, obs.records, 1)
	assert.Equal(t, loggerMessage, obs.records[0].Message)
	assert.Equal(t, slog.LevelInfo, obs.records[0].Level)
	assert.Equal(t, "log", obs.records[0].Attrs["test"])
}

func TestLoggerWithDefaultOptions(t *testing.T) {
	logger, obs := newTestLogger(slog.LevelInfo)
	options := &LoggerOptions{Logger: logger}

	handler := LoggerWithOptions(options)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, obs.records, 1)
	assert.Equal(t, loggerMessage, obs.records[0].Message)
	assert.Equal(t, "GET", obs.records[0].Attrs["method"])
}

func TestLoggerWithoutRequestID(t *testing.T) {
	logger, obs := newTestLogger(slog.LevelInfo)

	handler := LoggerWithOptions(&LoggerOptions{Logger: logger})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, obs.records, 1)
	assert.Equal(t, uuid.Nil.String(), obs.records[0].Attrs["req_id"])
}

func TestLoggerWithRequestID(t *testing.T) {
	logger, obs := newTestLogger(slog.LevelInfo)

	handler := LoggerWithOptions(&LoggerOptions{Logger: logger})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	reqID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	req = req.WithContext(context.WithValue(req.Context(), httputil.RequestIDCtxKey, reqID))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, obs.records, 1)
	assert.Equal(t, reqID, obs.records[0].Attrs["req_id"])
}

func TestLoggerLevelByStatusCode(t *testing.T) {
	cases := []struct {
		status    int
		wantLevel slog.Level
	}{
		{http.StatusOK, slog.LevelInfo},
		{http.StatusNotFound, slog.LevelWarn},
		{http.StatusInternalServerError, slog.LevelError},
	}

	for _, tc := range cases {
		logger, obs := newTestLogger(slog.LevelInfo)
		handler := LoggerWithOptions(&LoggerOptions{Logger: logger})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		require.Len(t, obs.records, 1)
		assert.Equal(t, tc.wantLevel, obs.records[0].Level, "status %d", tc.status)
	}
}
