package httputil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	// "github.com/zitadel/oidc/pkg/oidc"
)

// Conn returns the user and pgxpool.Conn retrieved from the request context.
func Conn(r *http.Request) (map[string]any, *pgxpool.Conn, *pgconn.PgError) {
	var user map[string]any
	var ok bool

	user, ok = r.Context().Value(OIDCUserCtxKey).(map[string]any)

	// If no OIDC user, try basic auth
	if !ok || user == nil {
		if basicUser, basicOk := r.Context().Value(BasicAuthCtxKey).(string); basicOk && basicUser != "" {
			// minimal OIDC object for basic auth
			user = map[string]any{
				// Active:  true,
				// Subject: basicUser,
				// Claims: map[string]any{
				"sub":      basicUser,
				"username": basicUser,
				// },
			}
		} else {
			anonRole, anonOk := r.Context().Value(OIDCRoleClaimCtxKey).(string)
			if !anonOk || anonRole == "" {
				return nil, nil, &pgconn.PgError{
					Code:    "28000",
					Message: "User not found in context and anonymous role not configured",
				}
			}

			// minimal OIDC object for anon user
			user = map[string]any{
				// Active:  true,
				// Subject: "anon",
				// Claims: map[string]any{
				"sub":  "anon",
				"role": anonRole,
				// },
			}
		}
	}

	conn, ok := r.Context().Value(PgConnCtxKey).(*pgxpool.Conn)
	if !ok || conn == nil {
		return nil, nil, &pgconn.PgError{
			Code:    "08003", // SQLSTATE for connection does not exist
			Message: "Failed to get connection from context",
		}
	}

	return user, conn, nil
}

// ConnWithRole retrieves the OIDC user and a pooled database connection, then
// sets the appropriate PostgreSQL role based on the request context. It's designed
// for use with Row Level Security (RLS) enabled tables.
//
// The function also sets JWT claims in the PostgreSQL session using the environment
// variable PIGO_POSTGRES_OIDC_REQUEST_JWT_CLAIMS (defaults to "request.jwt.claims"
// for PostgREST compatibility).
//
// Example RLS policy that allows users to only select their own rows:
//
//	ALTER TABLE wallets ENABLE ROW LEVEL SECURITY;
//	ALTER TABLE wallets FORCE ROW LEVEL SECURITY;
//	DROP POLICY IF EXISTS select_own ON wallets;
//	CREATE POLICY select_own ON wallets FOR SELECT USING (
//		user_id = (current_setting('request.jwt.claims', true)::json->>'sub')::TEXT
//	);
//	ALTER POLICY select_own ON wallets TO authn;
//
// For more information on how claims are used, see:
// https://docs.postgrest.org/en/v12/references/transactions.html#request-headers-cookies-and-jwt-claims
func ConnWithRole(r *http.Request) (map[string]any, *pgxpool.Conn, *pgconn.PgError) {
	user, conn, pgErr := Conn(r)
	if pgErr != nil {
		return nil, nil, pgErr
	}

	var role string

	if roleVal, ok := r.Context().Value(OIDCRoleClaimCtxKey).(string); ok && roleVal != "" {
		role = roleVal
	} else {
		// Fallback for basic auth: use username as role
		if basicUser, ok := r.Context().Value(BasicAuthCtxKey).(string); ok && basicUser != "" {
			role = basicUser
		} else if anonRole := os.Getenv("PIGO_POSTGRES_ANON_ROLE"); anonRole != "" {
			// Fallback for anon: use configured anon role
			role = anonRole
		} else {
			return nil, nil, &pgconn.PgError{
				Code:    "28000",
				Message: "Role not found in context",
			}
		}
	}

	claimsJSON, err := json.Marshal(user)
	if err != nil {
		return nil, nil, &pgconn.PgError{
			Code:    "28000",
			Message: fmt.Sprintf("Failed to marshal claims: %v", err),
		}
	}

	escapedClaimsJSON := strings.ReplaceAll(string(claimsJSON), "'", "''")
	setRoleQuery := fmt.Sprintf("SET ROLE %s;", role)
	reqClaims, ok := os.LookupEnv("PIGO_POSTGRES_OIDC_REQUEST_JWT_CLAIMS")
	if !ok {
		reqClaims = "request.jwt.claims"
	}
	setReqClaimsQuery := fmt.Sprintf("SET %s TO '%s';", reqClaims, escapedClaimsJSON)
	combinedQuery := setRoleQuery + setReqClaimsQuery

	_, execErr := conn.Exec(context.Background(), combinedQuery)
	if execErr != nil {
		conn.Release()
		if pgErr, ok := execErr.(*pgconn.PgError); ok {
			return nil, nil, pgErr
		}
		return nil, nil, &pgconn.PgError{
			Code:    "P0000", // Generic SQLSTATE code
			Message: "Failed to set role and claims",
		}
	}

	return user, conn, nil
}
