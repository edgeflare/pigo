package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"go.uber.org/zap"
)

// AuthType represents supported authentication methods
type AuthType string

const (
	AuthTypeNone       AuthType = "none"
	AuthTypeAPIKey     AuthType = "apikey"
	AuthTypeBearer     AuthType = "bearer"
	AuthTypeBasic      AuthType = "basic"
	AuthTypeOAuth2     AuthType = "oauth2"
	AuthTypeGCPSA      AuthType = "gcp_service_account"
	AuthTypeAWSIAM     AuthType = "aws_iam"
	AuthTypeCloudflare AuthType = "cloudflare"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Type AuthType `json:"type"`
	// API Key settings
	APIKey     string `json:"apiKey,omitempty"`
	APIKeyName string `json:"apiKeyName,omitempty"` // Header name for API key
	// Basic auth settings
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// OAuth2/Bearer settings
	Token        string `json:"token,omitempty"`
	TokenFile    string `json:"tokenFile,omitempty"`    // Path to token file
	ClientID     string `json:"clientId,omitempty"`     // OAuth2 client ID
	ClientSecret string `json:"clientSecret,omitempty"` // OAuth2 client secret
	TokenURL     string `json:"tokenUrl,omitempty"`     // OAuth2 token endpoint
	Scopes       string `json:"scopes,omitempty"`       // OAuth2 scopes
	// GCP Service Account settings
	ServiceAccountFile string `json:"serviceAccountFile,omitempty"`
	// AWS IAM settings
	AWSRegion          string `json:"awsRegion,omitempty"`
	AWSAccessKeyID     string `json:"awsAccessKeyId,omitempty"`
	AWSSecretAccessKey string `json:"awsSecretAccessKey,omitempty"`
	// Cloudflare settings
	CloudflareToken string `json:"cloudflareToken,omitempty"`
	CloudflareKey   string `json:"cloudflareKey,omitempty"`
	CloudflareEmail string `json:"cloudflareEmail,omitempty"`
}

// RetryConfig holds retry settings for failed webhook attempts
type RetryConfig struct {
	MaxRetries  int           `json:"maxRetries"`
	InitialWait time.Duration `json:"initialWait"`
	MaxWait     time.Duration `json:"maxWait"`
}

// EndpointConfig represents configuration for a single endpoint
type EndpointConfig struct {
	Headers map[string]string `json:"headers,omitempty"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
}

// PeerHTTP implements HTTP webhook functionality
type PeerHTTP struct {
	client *http.Client
	logger *zap.Logger
	auth   AuthConfig
	pipeline.Peer
	endpoints   []EndpointConfig
	retryConfig RetryConfig
}

// Connect initializes the HTTP client with the provided configuration
func (p *PeerHTTP) Connect(config json.RawMessage, args ...any) error {
	var cfg struct {
		Auth      AuthConfig       `json:"auth"`
		Timeout   string           `json:"timeout"`
		Endpoints []EndpointConfig `json:"endpoints"`
		Retry     RetryConfig      `json:"retry"`
	}

	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal HTTP config: %w", err)
	}

	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("no endpoints configured")
	}

	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout duration: %w", err)
		}
		timeout = parsedTimeout
	}

	p.setDefaultConfig(&cfg)
	p.client = &http.Client{Timeout: timeout}
	p.endpoints = cfg.Endpoints
	p.auth = cfg.Auth
	p.retryConfig = cfg.Retry

	if err := p.validateConfig(); err != nil {
		return err
	}

	p.logger.Info("HTTP peer initialized",
		zap.Int("num_endpoints", len(cfg.Endpoints)),
		zap.String("auth_type", string(cfg.Auth.Type)),
		zap.Duration("timeout", timeout))

	return nil
}

func (p *PeerHTTP) setDefaultConfig(cfg *struct {
	Auth      AuthConfig       `json:"auth"`
	Timeout   string           `json:"timeout"`
	Endpoints []EndpointConfig `json:"endpoints"`
	Retry     RetryConfig      `json:"retry"`
}) {
	// Set default retry config
	if cfg.Retry.MaxRetries == 0 {
		cfg.Retry.MaxRetries = 3
	}
	if cfg.Retry.InitialWait == 0 {
		cfg.Retry.InitialWait = 1 * time.Second
	}
	if cfg.Retry.MaxWait == 0 {
		cfg.Retry.MaxWait = 30 * time.Second
	}

	// Set default method for endpoints
	for i := range cfg.Endpoints {
		if cfg.Endpoints[i].Method == "" {
			cfg.Endpoints[i].Method = http.MethodPost
		}
	}

	// Default to no auth if not specified
	if cfg.Auth.Type == "" {
		cfg.Auth.Type = AuthTypeNone
	}
}

func (p *PeerHTTP) validateConfig() error {
	switch p.auth.Type {
	case AuthTypeAPIKey:
		if p.auth.APIKey == "" {
			return fmt.Errorf("API key authentication requires an API key")
		}
		if p.auth.APIKeyName == "" {
			p.auth.APIKeyName = "X-API-Key" // default header name
		}
	case AuthTypeBasic:
		if p.auth.Username == "" || p.auth.Password == "" {
			return fmt.Errorf("basic authentication requires both username and password")
		}
	case AuthTypeBearer:
		if p.auth.Token == "" && p.auth.TokenFile == "" {
			return fmt.Errorf("bearer authentication requires either token or token file")
		}
	case AuthTypeOAuth2:
		if p.auth.ClientID == "" || p.auth.ClientSecret == "" || p.auth.TokenURL == "" {
			return fmt.Errorf("OAuth2 authentication requires client ID, client secret, and token URL")
		}
	case AuthTypeGCPSA:
		if p.auth.ServiceAccountFile == "" {
			return fmt.Errorf("GCP service account authentication requires service account file")
		}
	case AuthTypeAWSIAM:
		if (p.auth.AWSAccessKeyID == "" || p.auth.AWSSecretAccessKey == "") && p.auth.AWSRegion == "" {
			return fmt.Errorf("AWS IAM authentication requires access key ID, secret access key, and region")
		}
	case AuthTypeCloudflare:
		if p.auth.CloudflareToken == "" && (p.auth.CloudflareKey == "" || p.auth.CloudflareEmail == "") {
			return fmt.Errorf("Cloudflare authentication requires either API token or API key with email")
		}
	}
	return nil
}

// Pub sends the CDC event as a webhook to configured endpoints
func (p *PeerHTTP) Pub(event cdc.Event, args ...any) error {
	payload, err := json.Marshal(event.Payload.After)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	var lastErr error
	for _, endpoint := range p.endpoints {
		config := httputil.DefaultRequestConfig(endpoint.Method, endpoint.URL)
		config.Headers = p.buildHeaders(endpoint)
		config.Timeout = p.client.Timeout
		// config.Logger = p.logger
		config.RetryEnabled = true
		config.MaxRetries = p.retryConfig.MaxRetries
		config.InitialBackoff = p.retryConfig.InitialWait
		config.MaxBackoff = p.retryConfig.MaxWait

		resp, err := httputil.Request(context.Background(), config, payload)
		if err != nil {
			lastErr = err
			p.logger.Error("failed to send webhook",
				zap.String("endpoint", endpoint.URL),
				zap.Error(err))
			continue
		}

		// Additional response processing if needed
		if resp.StatusCode >= 400 {
			p.logger.Warn("webhook returned error status",
				zap.String("endpoint", endpoint.URL),
				zap.Int("status", resp.StatusCode),
				zap.String("body", string(resp.Body)))
		}
	}

	return lastErr
}

func (p *PeerHTTP) buildHeaders(endpoint EndpointConfig) map[string][]string {
	headers := make(map[string][]string)

	for key, value := range endpoint.Headers {
		headers[key] = []string{value}
	}

	switch p.auth.Type {
	case AuthTypeAPIKey:
		headers[p.auth.APIKeyName] = []string{p.auth.APIKey}
	case AuthTypeBasic:
		headers["Authorization"] = []string{"Basic " + basicAuth(p.auth.Username, p.auth.Password)}
	case AuthTypeBearer:
		headers["Authorization"] = []string{"Bearer " + p.auth.Token}
	case AuthTypeCloudflare:
		if p.auth.CloudflareToken != "" {
			headers["Authorization"] = []string{"Bearer " + p.auth.CloudflareToken}
		} else {
			headers["X-Auth-Key"] = []string{p.auth.CloudflareKey}
			headers["X-Auth-Email"] = []string{p.auth.CloudflareEmail}
		}
	}

	return headers
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (p *PeerHTTP) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePub
}

func (p *PeerHTTP) Sub(args ...any) (<-chan cdc.Event, error) {
	// TODO: implement
	// Built-in web server capable of handling incoming HTTP requests
	// construct CDC from request url, query params, body etc
	return nil, pipeline.ErrConnectorTypeMismatch
}

func (p *PeerHTTP) Disconnect() error {
	return nil
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorHTTP, &PeerHTTP{})
}
