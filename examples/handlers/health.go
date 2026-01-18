package handlers

import (
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
)

// HealthHandler returns the request ID as a plain text response with a 200 OK status code.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	requestID := r.Context().Value(httputil.RequestIDCtxKey).(string)
	if requestID == "" {
		http.Error(w, "Request ID not found", http.StatusInternalServerError)
		return
	}

	httputil.Text(w, http.StatusOK, requestID)
}
