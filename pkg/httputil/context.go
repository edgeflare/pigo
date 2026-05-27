package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

type contextKey string

// Context keys for values stored by middleware.
var (
	RequestIDCtxKey     = contextKey("RequestID")
	LogEntryCtxKey      = contextKey("LogEntry")
	OIDCUserCtxKey      = contextKey("OIDCUser")
	BasicAuthCtxKey     = contextKey("BasicAuth")
	PgConnCtxKey        = contextKey("PgConn")
	OIDCRoleClaimCtxKey = contextKey("OIDCRoleClaim")
)

func OIDCUser(r *http.Request) (map[string]any, bool) {
	claims, ok := r.Context().Value(OIDCUserCtxKey).(map[string]any)
	if !ok || claims == nil {
		return nil, false
	}
	return claims, true
}

// BasicAuthUser retrieves the authenticated username from the context.
func BasicAuthUser(r *http.Request) (string, bool) {
	user, ok := r.Context().Value(BasicAuthCtxKey).(string)
	return user, ok
}

const defaultMaxBodyBytes int64 = 1 << 20

type bindOptions struct {
	maxBytes int64
}

// BindOption configures Bind behaviour.
type BindOption func(*bindOptions)

// WithMaxBytes sets the body size limit for Bind. Default is 1 MiB.
func WithMaxBytes(n int64) BindOption {
	return func(o *bindOptions) { o.maxBytes = n }
}

// Bind decodes a JSON request body into dst, capped at 1 MiB by default.
// For strict decoding (no unknown fields), configure the decoder directly.
//
// Override the limit with WithMaxBytes:
//
//	if err := httputil.Bind(r, &dst, httputil.WithMaxBytes(10<<20)); err != nil {
//	    httputil.Error(w, http.StatusBadRequest, err.Error())
//	    return
//	}
func Bind(r *http.Request, dst any, opts ...BindOption) error {
	o := bindOptions{maxBytes: defaultMaxBodyBytes}
	for _, opt := range opts {
		opt(&o)
	}
	r.Body = http.MaxBytesReader(nil, r.Body, o.maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return fmt.Errorf("request body exceeds %d bytes", o.maxBytes)
		}
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

// JSON marshals v to JSON and writes it with the given status code.
//
// Marshalling happens before WriteHeader so that a marshalling failure can
// still be reported as a 500 without having already committed a 200.
func JSON(w http.ResponseWriter, statusCode int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		// Safe to call Error here: WriteHeader has not been called yet.
		Error(w, http.StatusInternalServerError, "failed to marshal response")
		slog.Error("httputil.JSON: marshal failed", "err", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if _, err := w.Write(b); err != nil {
		// Headers are sent; nothing useful we can do except log.
		slog.Error("httputil.JSON: write failed", "err", err)
	}
}

// Text writes a plain-text response with the given status code.
func Text(w http.ResponseWriter, statusCode int, text string) {
	writeBody(w, statusCode, "text/plain; charset=utf-8", []byte(text))
}

// HTML writes an HTML response with the given status code.
func HTML(w http.ResponseWriter, statusCode int, html string) {
	writeBody(w, statusCode, "text/html; charset=utf-8", []byte(html))
}

// Blob writes a binary response with the given status code and content type.
func Blob(w http.ResponseWriter, statusCode int, contentType string, data []byte) {
	writeBody(w, statusCode, contentType, data)
}

// ErrorResponse is the JSON shape returned by Error.
// The HTTP status code is already in the response line; Code is included here
// so API clients that only inspect the body don't need to track it separately.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error writes a JSON error response. It is safe to call before WriteHeader has
// been invoked; calling it after is a no-op on the status (already sent) but
// will still attempt to write the body.
func Error(w http.ResponseWriter, statusCode int, message string) {
	b, err := json.Marshal(ErrorResponse{Code: statusCode, Message: message})
	if err != nil {
		// Absolute last resort: fall back to plain text.
		http.Error(w, message, statusCode)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(b) // error unrecoverable after WriteHeader
}

// writeBody is the shared implementation for Text, HTML, and Blob.
// Separating it avoids repeating the post-WriteHeader logging pattern.
func writeBody(w http.ResponseWriter, statusCode int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(statusCode)
	if _, err := io.Copy(w, bytes.NewReader(body)); err != nil {
		slog.Error("httputil: write body failed", "content_type", contentType, "err", err)
	}
}
