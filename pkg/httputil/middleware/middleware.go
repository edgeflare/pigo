package middleware

import (
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
)

// Add applies one or more middleware functions to a handler in the order they were provided.
// The first middleware in the list will be the outermost wrapper (executed first).
func Add(h http.Handler, middlewares ...httputil.Middleware) http.Handler {
	// Apply middlewares in reverse order so that the first middleware
	// in the list is the outermost wrapper (executed first)
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
