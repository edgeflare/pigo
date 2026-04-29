package httputil

import (
	"context"
	"crypto/tls"
	"fmt"

	"log"
	"net/http"
	"strings"
	"sync"

	"slices"

	"github.com/edgeflare/pigo/pkg/util"
)

// Middleware defines a function type that represents a middleware. Middleware functions wrap an
// http.Handler to modify or enhance its behavior.
type Middleware func(http.Handler) http.Handler

// RouterOptions is a function type that represents options to configure a Router.
type RouterOptions func(*Router)

// Router is the main structure for handling HTTP routing and middleware.
type Router struct {
	mux        *http.ServeMux
	server     *http.Server
	prefix     string
	middleware []Middleware
	mu         sync.RWMutex
}

// NewRouter creates a new instance of Router with the given options.
func NewRouter(opts ...RouterOptions) *Router {
	r := &Router{
		mux:    http.NewServeMux(),
		server: &http.Server{}, // Initialize with default server
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// WithServerOptions returns a RouterOptions function that sets custom http.Server options.
func WithServerOptions(opts ...func(*http.Server)) RouterOptions {
	return func(r *Router) {
		for _, opt := range opts {
			opt(r.server)
		}
	}
}

// WithTLS provides a simplified way to enable HTTPS in your router.
func WithTLS(certFile, keyFile string) RouterOptions {
	return func(r *Router) {
		r.server.TLSConfig = &tls.Config{}               // Initialize TLS config
		r.server.TLSConfig.MinVersion = tls.VersionTLS12 // Enforce secure TLS version (optional)

		var cert tls.Certificate
		var err error

		if certFile == "" || keyFile == "" {
			// Generate a self-signed certificate if paths are not provided
			cert, err = util.LoadOrGenerateCert("./tls/tls.crt", "./tls/tls.key")
			if err != nil {
				log.Fatalf("failed to generate self-signed certificate: %v", err)
			}
		} else {
			// Load certificate from provided paths
			cert, err = util.LoadOrGenerateCert(certFile, keyFile)
			if err != nil {
				log.Fatalf("error loading TLS certificates: %v", err)
			}
		}

		r.server.TLSConfig.Certificates = []tls.Certificate{cert}
	}
}

// Use adds one or more middleware to the router. At least one middleware must be provided.
// Middleware functions are applied in the order they are added.
func (r *Router) Use(mw Middleware, additional ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, mw)
	if len(additional) > 0 {
		r.middleware = append(r.middleware, additional...)
	}
}

// Group creates a new sub-router with a specified prefix. The sub-router inherits the middleware
// from its parent router.
func (r *Router) Group(prefix string) *Router {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &Router{
		mux:        r.mux,
		middleware: slices.Clone(r.middleware),
		server:     r.server,
		prefix:     r.prefix + prefix,
	}
}

// Handle registers an HTTP handler function for a given method and pattern as introduced in
// [Routing Enhancements for Go 1.22](https://go.dev/blog/routing-enhancements)
// The handler `METHOD /pattern` on a route group with a /prefix resolves to `METHOD /prefix/pattern`
func (r *Router) Handle(methodPattern string, handler http.Handler) {
	parts := strings.SplitN(methodPattern, " ", 2)
	if len(parts) != 2 {
		log.Fatalf("invalid method pattern: %s", methodPattern)
	}
	method, pattern := parts[0], parts[1]

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create the final handler with all middleware applied
	finalHandler := handler
	for i := len(r.middleware) - 1; i >= 0; i-- {
		finalHandler = r.middleware[i](finalHandler)
	}
	// fullPattern := r.prefix + pattern
	fullPattern := fmt.Sprintf("%s %s%s", method, r.prefix, pattern)

	r.mux.Handle(fullPattern, finalHandler)
}

// ListenAndServe starts the server, automatically choosing between HTTP and HTTPS based on TLS config.
func (r *Router) ListenAndServe(addr string) error {
	fmt.Print(colorGreen + pigoASCIIArt + colorReset)
	fmt.Printf("starting server on %s\n", addr)

	r.server.Addr = addr
	r.server.Handler = r.applyMiddleware()

	if r.server.TLSConfig != nil {
		// HTTPS
		return r.server.ListenAndServeTLS("", "") // Use empty strings to auto-detect cert/key in TLSConfig
	}
	// HTTP
	return r.server.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (r *Router) Shutdown(ctx context.Context) error {
	log.Println("shutting down server")
	return r.server.Shutdown(ctx)
}

// applyMiddleware applies middleware to the http.Handler and returns a new http.Handler.
func (r *Router) applyMiddleware() http.Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var handler http.Handler = r.mux
	for i := len(r.middleware) - 1; i >= 0; i-- {
		handler = r.middleware[i](handler)
	}
	return handler
}

// Constants for ASCII art and console colors
const (
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorReset   = "\033[0m"
	pigoASCIIArt = `

 _ __ (_)  _ _  __
| '_ \| |/ _' |/ _ \
| |_) | | (_| | (_) |
| .__/|_|\__, |\___/
|_|      |___/

`
)
