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

// Router handles HTTP routing and middleware chaining.
// It implements http.Handler and can therefore be used with any standard
// net/http server, httptest.NewServer, or embedded in other handlers.
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
	if strings.HasSuffix(prefix, "/") {
		panic(fmt.Sprintf("httputil: group prefix %q must not end with '/'", prefix))
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return &Router{
		mux:        r.mux,
		middleware: slices.Clone(r.middleware),
		server:     r.server,
		prefix:     r.prefix + prefix,
	}
}

// Handle registers handler for "METHOD /path" patterns (Go 1.22+). Panics on malformed input.
func (r *Router) Handle(methodPattern string, handler http.Handler) {
	method, pattern := splitMethodPattern(methodPattern)
	r.mu.RLock()
	prefix := r.prefix
	mw := slices.Clone(r.middleware)
	r.mu.RUnlock()
	fullPattern := fmt.Sprintf("%s %s%s", method, prefix, pattern)
	r.mux.Handle(fullPattern, applyMiddleware(handler, mw))
}

// HandleFunc is a convenience wrapper around Handle for plain functions.
func (r *Router) HandleFunc(methodPattern string, handler http.HandlerFunc) {
	r.Handle(methodPattern, handler)
}

// ListenAndServe starts the HTTP (or HTTPS if TLS is configured) server on addr.
// It sets the router itself as the server's Handler so that ServeHTTP is the
// single entry point.
func (r *Router) ListenAndServe(addr string) error {
	fmt.Print(colorGreen + pigoASCIIArt + colorReset)
	log.Printf("starting server on %s", addr)

	r.server.Addr = addr
	r.server.Handler = r

	if r.server.TLSConfig != nil {
		// Certificates are already in TLSConfig; pass empty strings so the
		// stdlib reads them from there rather than from files.
		return r.server.ListenAndServeTLS("", "")
	}
	return r.server.ListenAndServe()
}

// ServeHTTP implements http.Handler, allowing Router to be used anywhere
// an http.Handler is accepted - httptest.NewServer, http.ListenAndServe, etc.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler().ServeHTTP(w, req)
}

// Shutdown gracefully shuts down the HTTP server.
func (r *Router) Shutdown(ctx context.Context) error {
	log.Println("shutting down server")
	return r.server.Shutdown(ctx)
}

// handler returns the mux wrapped with all registered middleware.
func (r *Router) handler() http.Handler {
	r.mu.RLock()
	mw := slices.Clone(r.middleware)
	r.mu.RUnlock()
	return applyMiddleware(r.mux, mw)
}

// applyMiddleware wraps h with each middleware in reverse order so that the
// first element in the slice is the outermost (first to run) handler.
func applyMiddleware(h http.Handler, mw []Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// splitMethodPattern splits "METHOD /path" into parts, panicking on malformed input.
func splitMethodPattern(s string) (method, pattern string) {
	method, pattern, ok := strings.Cut(s, " ")
	if !ok || !strings.HasPrefix(pattern, "/") {
		panic(fmt.Sprintf("httputil: invalid pattern %q: expected \"METHOD /path\"", s))
	}
	return method, pattern
}

// Constants for ASCII art and console colors
const (
	// colorRed     = "\033[31m"
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
