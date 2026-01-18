package middleware

import (
	"context"
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-Id"

// RequestID middleware generates a unique request ID and tracks request duration.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if request ID is already set in the context
		reqID, ok := r.Context().Value(httputil.RequestIDCtxKey).(string)
		if !ok || reqID == "" {
			reqID = uuid.New().String()
		}

		ctx := r.Context()
		// Not sure whether storing the request ID in the context is useful
		// currently used by the logger middleware, but it can read from the request header set by this middleware
		ctx = context.WithValue(ctx, httputil.RequestIDCtxKey, reqID)
		w.Header().Set(RequestIDHeader, reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
