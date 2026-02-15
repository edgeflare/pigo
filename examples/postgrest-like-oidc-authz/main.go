package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edgeflare/pigo/pkg/httputil"
	mw "github.com/edgeflare/pigo/pkg/httputil/middleware"
	"github.com/edgeflare/pigo/pkg/pgx"
)

func main() {
	port := flag.Int("port", 8080, "port to run the server on")
	flag.Parse()

	r := httputil.NewRouter()

	// Group with prefix "/api/v1"
	apiv1 := r.Group("/api/v1")

	// optional middleware with default options
	apiv1.Use(mw.RequestID, mw.LoggerWithOptions(nil), mw.CORSWithOptions(nil))

	// OIDC middleware for authentication
	oidcConfig := mw.OIDCProviderConfig{
		ClientID:     os.Getenv("PIGO_OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("PIGO_OIDC_CLIENT_SECRET"),
		Issuer:       os.Getenv("PIGO_OIDC_ISSUER"),
	}
	apiv1.Use(mw.VerifyOIDCToken(oidcConfig))

	pgxPoolMgr := pgx.NewPoolManager()
	err := pgxPoolMgr.Add(context.Background(), pgx.Pool{Name: "default", ConnString: os.Getenv("PIGO_POSTGRES_CONN_STRING")}, true)
	if err != nil {
		log.Fatal(err)
	}

	pgxPool, err := pgxPoolMgr.Active()
	if err != nil {
		log.Fatal(err)
	}

	// Use Postgres middleware to attach a pgxpool.Conn to the request context for authorized users
	pgmw := mw.Postgres(pgxPool, mw.WithOIDCAuthz(
		oidcConfig,
		cmp.Or(os.Getenv("PIGO_POSTGRES_OIDC_ROLE_CLAIM_KEY"), ".policy.pgrole")),
	)
	apiv1.Use(pgmw)

	// Below `GET /api/v1/mypgrole` queries for, and responds with,
	// session_user, current_user using the pgxpool.Conn attached by the Postgres middleware
	apiv1.Handle("GET /mypgrole", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, conn, err := httputil.ConnWithRole(r)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, err.Error())
		}
		defer conn.Release()

		var session_user, current_user string
		pgErr := conn.QueryRow(r.Context(), "SELECT session_user").Scan(&session_user)
		if pgErr != nil {
			httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		}
		pgErr = conn.QueryRow(r.Context(), "SELECT current_user").Scan(&current_user)
		if pgErr != nil {
			httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		}

		role := map[string]string{
			// role with which initial connection to database is established
			"session_user": session_user,
			// role with which application query is performed to process this particular request
			"current_user": current_user,
			"user_sub":     user["sub"].(string),
		}

		httputil.JSON(w, http.StatusOK, role)
	}))

	// Run server in a goroutine
	go func() {
		if err := r.ListenAndServe(fmt.Sprintf(":%d", *port)); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	fmt.Printf("Server is running on port %d\n", *port)

	// Set up signal handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Wait for SIGINT or SIGTERM
	<-stop

	fmt.Println("Shutting down server...")

	// Create a deadline for the shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := r.Shutdown(ctx); err != nil {
		fmt.Printf("server forced to shutdown: %s", err)
	}
	fmt.Println("Server gracefully stopped")
}
