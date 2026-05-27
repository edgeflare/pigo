package middleware

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// CORSOptions defines configuration for CORS.
type CORSOptions struct {
	AllowedOrigins   []string // ["*"] for wildcard, or explicit origins
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string // e.g. ["Content-Range", "Prefer"]
	AllowCredentials bool     // must be false when AllowedOrigins is ["*"]
	MaxAge           int      // preflight cache in seconds, e.g. 86400
}

// defaultCORSOptions returns the default CORS options.
func defaultCORSOptions() *CORSOptions {
	return &CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Content-Type", "Content-Length", "Accept-Encoding",
			"X-CSRF-Token", "Authorization", "Accept", "Origin",
			"Cache-Control", "X-Requested-With",
		},
		ExposedHeaders:   []string{"Content-Range", "Prefer"},
		AllowCredentials: false, // cannot be true with wildcard origin
		MaxAge:           86400,
	}
}

// CORSWithOptions creates a CORS middleware with the provided configuration.
// If options is nil, it will use the default CORS settings.
// If options is an empty struct (CORSOptions{}), it will create a middleware with no CORS headers.
func CORSWithOptions(options *CORSOptions) func(http.Handler) http.Handler {
	if options == nil {
		options = defaultCORSOptions()
	}

	wildcard := len(options.AllowedOrigins) == 1 && options.AllowedOrigins[0] == "*"

	if wildcard && options.AllowCredentials {
		panic("httputil/middleware: AllowCredentials must be false when AllowedOrigins is [\"*\"]")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowedOrigin := ""
			if wildcard {
				allowedOrigin = "*"
			} else if slices.Contains(options.AllowedOrigins, origin) {
				allowedOrigin = origin
				w.Header().Add("Vary", "Origin")
			}

			if allowedOrigin == "" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)

			if options.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if len(options.ExposedHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
			}

			if r.Method == http.MethodOptions {
				if len(options.AllowedMethods) > 0 {
					w.Header().Set("Access-Control-Allow-Methods", strings.Join(options.AllowedMethods, ", "))
				}
				if len(options.AllowedHeaders) > 0 {
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(options.AllowedHeaders, ", "))
				}
				if options.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", options.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
