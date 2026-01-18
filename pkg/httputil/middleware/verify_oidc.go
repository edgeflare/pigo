package middleware

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/edgeflare/pigo/pkg/httputil"
	"golang.org/x/oauth2"
)

// OIDCProviderConfig holds the configuration for the OIDC provider
type OIDCProviderConfig struct {
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	Issuer        string `json:"issuer"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`
}

type OIDCProvider struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	config   OIDCProviderConfig
	cache    *Cache
}

var (
	oidcProvider *OIDCProvider
	oidcInitOnce sync.Once
)

// VerifyOIDCToken is middleware that verifies OIDC tokens in Authorization headers.
// By default, it sends a 401 Unauthorized response if the token is missing or invalid.
// If send401Unauthorized is false, it allows requests with other authorization schemes
// (e.g., Basic Auth) to continue without interference.
func VerifyOIDCToken(config OIDCProviderConfig, send401Unauthorized ...bool) func(http.Handler) http.Handler {
	send401 := true // Default behavior: Send 401 on failure
	if len(send401Unauthorized) > 0 {
		send401 = send401Unauthorized[0]
	}

	return func(next http.Handler) http.Handler {
		// Initialize OIDC provider once
		oidcInitOnce.Do(func() {
			if oidcProvider == nil {
				oidcProvider = initOIDCProvider(config)
			}
		})

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				if send401 {
					http.Error(w, "Authorization header is required", http.StatusUnauthorized)
					return
				}
				// No Authorization header and send401Unauthorized is false,
				// so let other middleware/handlers handle it
				next.ServeHTTP(w, r)
				return
			}

			// Check for "Bearer" token (case-insensitive)
			if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				if send401 {
					http.Error(w, "Authorization header must be Bearer token", http.StatusUnauthorized)
					return
				}
				// Other authorization scheme present and send401Unauthorized is false
				next.ServeHTTP(w, r)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			// Verify the token
			idToken, err := oidcProvider.verifier.Verify(r.Context(), tokenString)
			if err != nil {
				log.Printf("Failed to verify token: %v", err)
				http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
				return
			}

			// Extract claims from the token
			var claims map[string]any
			if err := idToken.Claims(&claims); err != nil {
				log.Printf("Failed to parse claims: %v", err)
				http.Error(w, "Failed to parse token claims", http.StatusInternalServerError)
				return
			}

			// Add claims to the request context
			ctx := context.WithValue(r.Context(), httputil.OIDCUserCtxKey, claims)

			// Call the next handler with the updated context
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func initOIDCProvider(config OIDCProviderConfig) *OIDCProvider {
	if config.ClientID == "" || config.ClientSecret == "" || config.Issuer == "" {
		panic("missing required OIDC configuration")
	}

	ctx := context.Background()

	// Custom HTTP client with TLS verification skipping if configured
	if config.SkipTLSVerify {
		insecureTransport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		insecureClient := &http.Client{
			Transport: insecureTransport,
		}
		// Use the insecure client for OIDC provider discovery
		ctx = oidc.ClientContext(ctx, insecureClient)
		ctx = context.WithValue(ctx, oauth2.HTTPClient, insecureClient)
		log.Println("Warning: TLS verification disabled for OIDC provider. This is not recommended for production use.")
	}

	// Create a new provider
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		log.Fatalf("Failed to create OIDC provider: %v", err)
	}

	// Create a verifier
	verifier := provider.Verifier(&oidc.Config{ClientID: config.ClientID})

	return &OIDCProvider{
		provider: provider,
		verifier: verifier,
		config:   config,
		cache:    NewCache(),
	}
}

/*
// migrate from zitadel/oidc to coreos/go-oidc

type OIDCProvider struct {
	provider rs.ResourceServer
	cache    *Cache
	config   OIDCProviderConfig
}

func VerifyOIDCToken(oidcCfg OIDCProviderConfig, send401Unauthorized ...bool) func(http.Handler) http.Handler {
	send401 := true // Default behavior: Send 401 on failure
	if len(send401Unauthorized) > 0 {
		send401 = send401Unauthorized[0]
	}

	return func(next http.Handler) http.Handler {
		oidcInitOnce.Do(func() {
			if oidcProvider == nil {
				oidcProvider = initOIDCProvider(oidcCfg)
			}
		})

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")

			if authHeader == "" {
				if send401 {
					http.Error(w, "Authorization header missing", http.StatusUnauthorized)
					return
				}
				// No Authorization header and send401Unauthorized is false,
				// so let other middleware/handlers handle it
				next.ServeHTTP(w, r)
				return
			}

			// Check for "Bearer" token (case-insensitive)
			if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				if send401 {
					http.Error(w, "Invalid token format", http.StatusUnauthorized)
					return
				}
				// Other authorization scheme present and send401Unauthorized is false
				next.ServeHTTP(w, r)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			user, err := rs.Introspect[*oidc.IntrospectionResponse](r.Context(), oidcProvider.provider, tokenString)
			if err != nil || user == nil {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), httputil.OIDCUserCtxKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func initOIDCProvider(cfg OIDCProviderConfig) *OIDCProvider {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.Issuer == "" {
		panic("missing required OIDC configuration")
	}

	provider, err := rs.NewResourceServerClientCredentials(context.Background(), cfg.Issuer, cfg.ClientID, cfg.ClientSecret)
	if err != nil {
		log.Fatalf("Failed to create OIDC provider: %v", err)
	}

	return &OIDCProvider{
		config:   cfg,
		provider: provider,
		cache:    NewCache(),
	}
}
*/
