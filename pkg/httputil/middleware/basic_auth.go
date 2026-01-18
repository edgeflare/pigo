package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/edgeflare/pigo/pkg/httputil"
)

// BasicAuthConfig holds the username-password pairs for basic authentication.
type BasicAuthConfig struct {
	Credentials map[string]string
}

// NewBasicAuthCreds creates a new instance of BasicAuthConfig with multiple username/password pairs.
func BasicAuthCreds(credentials map[string]string) *BasicAuthConfig {
	return &BasicAuthConfig{
		Credentials: credentials,
	}
}

// VerifyBasicAuth is a middleware function for basic authentication.
// By default, it sends a 401 Unauthorized response if credentials are missing or invalid.
// If send401Unauthorized is false, it allows requests without valid Basic Auth credentials
// to continue without interference.
func VerifyBasicAuth(config *BasicAuthConfig, send401Unauthorized ...bool) func(http.Handler) http.Handler {
	send401 := true // Default behavior: Send 401 on failure
	if len(send401Unauthorized) > 0 {
		send401 = send401Unauthorized[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				if send401 {
					w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
					http.Error(w, "Authorization header missing", http.StatusUnauthorized)
					return
				}
				// No Authorization header and send401Unauthorized is false,
				// so let other middleware/handlers handle it
				next.ServeHTTP(w, r)
				return
			}

			// Check if the authorization header is in the correct format
			if !strings.HasPrefix(authHeader, "Basic ") {
				if send401 {
					http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
					return
				}
				// Other authorization scheme present and send401Unauthorized is false
				next.ServeHTTP(w, r)
				return
			}

			// Decode the base64 encoded credentials
			encodedCredentials := strings.TrimPrefix(authHeader, "Basic ")
			credentials, err := base64.StdEncoding.DecodeString(encodedCredentials)
			if err != nil {
				if send401 {
					http.Error(w, "Invalid base64 encoding", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Split the credentials into username and password
			creds := strings.SplitN(string(credentials), ":", 2)
			if len(creds) != 2 {
				if send401 {
					http.Error(w, "Invalid credentials format", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			username, password := creds[0], creds[1]

			// Verify the credentials
			if validPassword, ok := config.Credentials[username]; !ok || validPassword != password {
				if send401 {
					w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
					http.Error(w, "Invalid credentials", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Store authenticated user in context
			ctx := context.WithValue(r.Context(), httputil.BasicAuthCtxKey, username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
