package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/spf13/viper"
)

// Config holds application-wide configuration
type Config struct {
	REST     RESTConfig      `mapstructure:"rest"`
	Pipeline pipeline.Config `mapstructure:"pipeline"`
}

type RESTConfig struct {
	PG         PGConfig          `mapstructure:"pg"`
	ListenAddr string            `mapstructure:"listenAddr"`
	BaseURL    string            `mapstructure:"baseURL"`
	OIDC       OIDCConfig        `mapstructure:"oidc"`
	BasicAuth  map[string]string `mapstructure:"basicAuth"`
	AnonRole   string            `mapstructure:"anonRole"`
	Omitempty  bool              `mapstructure:"omitempty"`
}

type PGConfig struct {
	ConnString string `mapstructure:"connString"`
}

type OIDCConfig struct {
	ClientID      string `mapstructure:"clientID"`
	ClientSecret  string `mapstructure:"clientSecret"`
	Issuer        string `mapstructure:"issuer"`
	SkipTLSVerify bool   `mapstructure:"skipTLSVerify"`
	RoleClaimKey  string `mapstructure:"roleClaimKey"`
}

// SetDefaults applies default values to viper
func SetDefaults(v *viper.Viper) {
	// REST defaults
	v.SetDefault("rest.listenAddr", ":8080")
	v.SetDefault("rest.anonRole", "anon")
	v.SetDefault("rest.oidc.roleClaimKey", ".policy.pgrole")
	v.SetDefault("rest.omitempty", false)
}

// Load reads config from file or environment
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	SetDefaults(v)

	// Try to load config file
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("error reading config file %s: %w", cfgFile, err)
		}
		fmt.Println("Using config file:", v.ConfigFileUsed())
	} else {
		// Look for default config locations
		v.SetConfigName("pigo")
		v.SetConfigType("yaml")
		if home, err := os.UserHomeDir(); err == nil {
			v.AddConfigPath(filepath.Join(home, ".config"))
		}
		v.AddConfigPath(".")

		// Try to read but don't fail if not found
		if err := v.ReadInConfig(); err == nil {
			fmt.Println("Using config file:", v.ConfigFileUsed())
		} else if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading default config: %w", err)
		}
	}

	// Override with environment variables
	v.AutomaticEnv()
	v.SetEnvPrefix("PGO")

	// CLI flags can override via viper.BindPFlag() elsewhere

	// Build the config
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// Handle special cases
	if cfg.REST.AnonRole == "" {
		cfg.REST.AnonRole = "anon"
	}

	return &cfg, nil
}

// Version is the current version of the application
var Version = `v0.0.1-experimental` // should be overridden by build process, ldflags
