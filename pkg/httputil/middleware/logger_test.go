package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func newTestLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	return logger, logs
}

func TestGetLogEntryMetadata(t *testing.T) {
	ctx := context.WithValue(context.Background(), httputil.LogEntryCtxKey, map[string]interface{}{"foo": "bar"})
	metadata := GetLogEntryMetadata(ctx)
	require.NotNil(t, metadata)
	assert.Equal(t, "bar", metadata["foo"])

	ctx = context.Background()
	metadata = GetLogEntryMetadata(ctx)
	assert.Nil(t, metadata)
}

func TestLoggerWithOptions(t *testing.T) {
	logger, logs := newTestLogger()
	options := &LoggerOptions{
		Logger: logger,
		Format: func(reqID string, rec *ResponseRecorder, r *http.Request, latency time.Duration) []zap.Field {
			return []zap.Field{
				zap.String("test", "log"),
			}
		},
	}
	middleware := LoggerWithOptions(options)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, "response", logs.All()[0].Message)
	assert.Equal(t, "log", logs.All()[0].ContextMap()["test"])
}

func TestLoggerWithDefaultOptions(t *testing.T) {
	logger, logs := newTestLogger()
	defaultLogger = logger
	middleware := LoggerWithOptions(nil)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, "response", logs.All()[0].Message)
	assert.Equal(t, "GET", logs.All()[0].ContextMap()["method"])
}

func TestLoggerWithoutRequestID(t *testing.T) {
	logger, logs := newTestLogger()
	options := &LoggerOptions{
		Logger: logger,
	}
	middleware := LoggerWithOptions(options)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, "response", logs.All()[0].Message)
	assert.Equal(t, uuid.Nil.String(), logs.All()[0].ContextMap()["req_id"])
}

func TestLoggerWithRequestID(t *testing.T) {
	logger, logs := newTestLogger()
	options := &LoggerOptions{
		Logger: logger,
	}
	middleware := LoggerWithOptions(options)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	reqID := uuid.New().String()
	ctx := context.WithValue(req.Context(), httputil.RequestIDCtxKey, reqID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, logs.Len())
	assert.Equal(t, "response", logs.All()[0].Message)
	assert.Equal(t, reqID, logs.All()[0].ContextMap()["req_id"])
}
