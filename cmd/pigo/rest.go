package pigo

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mw "github.com/edgeflare/pigo/pkg/httputil/middleware"
	"github.com/edgeflare/pigo/pkg/rest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var restCmd = &cobra.Command{
	Use:   "rest",
	Short: "Start the REST API server",
	Long:  `Starts a REST API server that provides access to PostgreSQL data through HTTP endpoints`,
	Run:   runRESTServer,
}

func init() {
	f := restCmd.Flags()
	f.StringP("rest.pg.connString", "c", "", "PostgreSQL connection string")
	f.StringP("rest.listenAddr", "l", "", "REST server listen address")
	f.String("rest.baseURL", "", "Base URL for API endpoints")
	f.String("rest.oidc.clientID", "", "OIDC client ID")
	f.String("rest.oidc.clientSecret", "", "OIDC client secret")
	f.String("rest.oidc.issuer", "", "OIDC issuer URL")
	f.BoolP("rest.oidc.skipTLSVerify", "s", false, "Skip TLS verification for OIDC issuer")
	f.String("rest.oidc.roleClaimKey", "", "JWT claim path for PostgreSQL role")
	f.String("rest.anonRole", "", "Anonymous PostgreSQL role")
	f.BoolP("rest.omitempty", "o", false, "Use omitempty for JSON responses")

	viper.BindPFlags(f)
	rootCmd.AddCommand(restCmd)
}

func runRESTServer(cmd *cobra.Command, args []string) {
	if cfg == nil {
		log.Fatal("Configuration not loaded")
	}

	// Override with environment variables for specific cases
	connString := cfg.REST.PG.ConnString
	if connString == "" {
		connString = os.Getenv("PIGO_REST_PG_CONN_STRING")
		if connString == "" {
			log.Fatal("PostgreSQL connection string required")
		}
	}

	oidcConfig := &mw.OIDCProviderConfig{
		ClientID:     cmp.Or(os.Getenv("PIGO_OIDC_CLIENT_ID"), cfg.REST.OIDC.ClientID),
		ClientSecret: cmp.Or(os.Getenv("PIGO_OIDC_CLIENT_SECRET"), cfg.REST.OIDC.ClientSecret),
		Issuer:       cmp.Or(os.Getenv("PIGO_OIDC_ISSUER"), cfg.REST.OIDC.Issuer),
		SkipTLSVerify: func() bool {
			if os.Getenv("PIGO_OIDC_SKIP_TLS_VERIFY") != "" {
				return true
			}
			return cfg.REST.OIDC.SkipTLSVerify
		}(),
	}

	roleClaimKey := cmp.Or(
		os.Getenv("PIGO_POSTGRES_OIDC_ROLE_CLAIM_KEY"),
		cfg.REST.OIDC.RoleClaimKey,
	)

	// Connect to database
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		log.Fatalf("Failed to create connection pool: %v", err)
	}

	// flag overrides
	listenAddr := viper.GetString("rest.listenAddr")
	if listenAddr != "" {
		cfg.REST.ListenAddr = listenAddr
	}

	// Create and configure server
	server, err := rest.NewServer(connString, cfg.REST.BaseURL, viper.Get("rest.omitempty").(bool) || cfg.REST.Omitempty)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// default middleware
	server.AddMiddleware(
		mw.RequestID,
		// mw.LoggerWithOptions(nil), // add logger middleware after all mw.AuthzFunc{}
		mw.CORSWithOptions(nil),
	)

	// Add OIDC auth if configured
	if oidcConfig.ClientID != "" && oidcConfig.Issuer != "" {
		server.AddMiddleware(mw.VerifyOIDCToken(*oidcConfig, false))
	}

	// Add basic auth if configured
	if len(cfg.REST.BasicAuth) > 0 {
		server.AddMiddleware(mw.VerifyBasicAuth(&mw.BasicAuthConfig{
			Credentials: cfg.REST.BasicAuth,
		}, false))
	}

	// Add Postgres auth middleware
	pgMiddleware := []mw.AuthzFunc{}

	// Add OIDC authz if configured
	if oidcConfig.ClientID != "" && oidcConfig.Issuer != "" {
		pgMiddleware = append(pgMiddleware, mw.WithOIDCAuthz(*oidcConfig, roleClaimKey))
	}

	// Add basic auth if configured
	if len(cfg.REST.BasicAuth) > 0 {
		pgMiddleware = append(pgMiddleware, mw.WithBasicAuthz())
	}

	// Add anon auth if configured
	if cfg.REST.AnonRole != "" {
		pgMiddleware = append(pgMiddleware, mw.WithAnonAuthz(cfg.REST.AnonRole))
	}

	server.AddMiddleware(mw.Postgres(pool, pgMiddleware...))

	// pg_role is set by the mw.AuthzFunc{} middleware. to include pg_role in request log, add logger middleware after all mw.AuthzFunc{}
	if logLevel != "none" {
		server.AddMiddleware(mw.LoggerWithOptions(nil))
	}
	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.Start(cfg.REST.ListenAddr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}

	log.Println("Server gracefully stopped")
}
