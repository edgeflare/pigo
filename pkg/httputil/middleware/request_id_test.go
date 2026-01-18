package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestRequestID(t *testing.T) {
	t.Run("should generate a new request ID if none exists", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Context().Value(httputil.RequestIDCtxKey).(string)
			_, err := uuid.Parse(reqID)
			assert.NoError(t, err, "Request ID should be a valid UUID")
		})

		reqIDMiddleware := RequestID(handler)

		req := httptest.NewRequest("GET", "http://example.com/foo", nil)
		w := httptest.NewRecorder()

		reqIDMiddleware.ServeHTTP(w, req)

		resp := w.Result()
		reqID := resp.Header.Get(RequestIDHeader)
		_, err := uuid.Parse(reqID)
		assert.NoError(t, err, "Response header X-Request-Id should be a valid UUID")
	})

	t.Run("should preserve existing request ID", func(t *testing.T) {
		existingReqID := uuid.New().String()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Context().Value(httputil.RequestIDCtxKey).(string)
			assert.Equal(t, existingReqID, reqID, "Request ID should match the existing ID")
		})

		reqIDMiddleware := RequestID(handler)

		// Create a request with a pre-set context containing a request ID
		ctx := context.WithValue(context.Background(), httputil.RequestIDCtxKey, existingReqID)
		req := httptest.NewRequest("GET", "http://example.com/foo", nil).WithContext(ctx)
		w := httptest.NewRecorder()

		reqIDMiddleware.ServeHTTP(w, req)

		resp := w.Result()
		reqID := resp.Header.Get(RequestIDHeader)
		assert.Equal(t, existingReqID, reqID, "Response header X-Request-Id should match the existing ID")
	})

	t.Run("should handle multiple requests independently", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Context().Value(httputil.RequestIDCtxKey).(string)
			w.Write([]byte(reqID))
		})

		reqIDMiddleware := RequestID(handler)

		// Test first request
		req1 := httptest.NewRequest("GET", "http://example.com/foo1", nil)
		w1 := httptest.NewRecorder()
		reqIDMiddleware.ServeHTTP(w1, req1)
		_ = w1.Result()
		body1 := w1.Body.String()

		// Test second request
		req2 := httptest.NewRequest("GET", "http://example.com/foo2", nil)
		w2 := httptest.NewRecorder()
		reqIDMiddleware.ServeHTTP(w2, req2)
		_ = w2.Result()
		body2 := w2.Body.String()

		assert.NotEqual(t, body1, body2, "Request IDs should be different for different requests")

		// Additional validation for request IDs in responses
		_, err1 := uuid.Parse(body1)
		_, err2 := uuid.Parse(body2)
		assert.NoError(t, err1, "Response body for first request should be a valid UUID")
		assert.NoError(t, err2, "Response body for second request should be a valid UUID")
	})
}
