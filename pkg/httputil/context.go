package httputil

import (
	"encoding/json"
	"net/http"
)

type ContextKey string

const (
	RequestIDCtxKey     ContextKey = "RequestID"
	LogEntryCtxKey      ContextKey = "LogEntry"
	OIDCUserCtxKey      ContextKey = "OIDCUser"
	BasicAuthCtxKey     ContextKey = "BasicAuth"
	PgConnCtxKey        ContextKey = "PgConn"
	OIDCRoleClaimCtxKey ContextKey = "OIDCRoleClaim"
)

func OIDCUser(r *http.Request) (map[string]any, bool) {
	claims, ok := r.Context().Value(OIDCUserCtxKey).(map[string]any)
	if !ok || claims == nil {
		return nil, false
	}
	return claims, true
}

// migrate from zitadel/oidc to coreos/go-oidc
// func OIDCUser(r *http.Request) (*oidc.IntrospectionResponse, bool) {
// 	user, ok := r.Context().Value(OIDCUserCtxKey).(*oidc.IntrospectionResponse)
// 	if !ok || user == nil {
// 		return nil, false
// 	}
// 	return user, true
// }

// BasicAuthUser retrieves the authenticated username from the context.
func BasicAuthUser(r *http.Request) (string, bool) {
	user, ok := r.Context().Value(BasicAuthCtxKey).(string)
	return user, ok
}

// Bind decodes the JSON body of an HTTP request and stores it in the value pointed to by dst.
func Bind(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// BindOrError decodes the JSON body of an HTTP request into the given destination object.
// If decoding fails, it responds with a 400 Bad Request error.
//
// Deprecated: Use Bind instead and handle errors manually. BindOrError will be removed in a future version.
// Migration example:
//
//	Old: if err := BindOrError(r, w, &dst); err != nil { return }
//	New: if err := Bind(r, &dst); err != nil { Error(w, http.StatusBadRequest, err.Error()); return }
func BindOrError(r *http.Request, w http.ResponseWriter, dst any) error {
	if err := Bind(r, dst); err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return err
	}
	return nil
}

// JSON writes a JSON response with the given status code and data.
func JSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ") // double-spaced pretty json. should take from config
	encoder.Encode(v)
}

// Text writes a plain text response with the given status code and text content.
func Text(w http.ResponseWriter, statusCode int, text string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(statusCode)
	if _, err := w.Write([]byte(text)); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

// HTML writes an HTML response with the given status code and HTML content.
func HTML(w http.ResponseWriter, statusCode int, html string) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(statusCode)
	if _, err := w.Write([]byte(html)); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

// Blob writes a binary response with the given status code and data.
func Blob(w http.ResponseWriter, statusCode int, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(statusCode)
	if _, err := w.Write(data); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

// ErrorResponse represents a structured error response.
type ErrorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Error sends a JSON response with an error code and message.
func Error(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errorResponse := ErrorResponse{
		Code:    statusCode,
		Message: message,
	}
	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		// Fallback if JSON encoding fails
		http.Error(w, "Failed to encode error response", http.StatusInternalServerError)
	}
}
