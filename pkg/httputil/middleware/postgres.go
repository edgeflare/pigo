package middleware

import (
	"context"
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres middleware attaches a connection from pool to the request context if the http request user is authorized.
func Postgres(pool *pgxpool.Pool, authorizers ...AuthzFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			for _, authorize := range authorizers {
				authzResponse, err := authorize(ctx)
				if err != nil {
					http.Error(w, "Authorization error", http.StatusInternalServerError)
					return
				}
				if authzResponse.Allowed {
					ctx = context.WithValue(ctx, httputil.OIDCRoleClaimCtxKey, authzResponse.Role)
					break
				}
			}

			if pgRole, ok := ctx.Value(httputil.OIDCRoleClaimCtxKey).(string); ok {
				// Acquire a connection from the default pool
				conn, err := pool.Acquire(r.Context())
				if err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
				// caller should
				// defer conn.Release()

				// set the connection in the context
				ctx = context.WithValue(ctx, httputil.PgConnCtxKey, conn)
				ctx = context.WithValue(ctx, httputil.OIDCRoleClaimCtxKey, pgRole)
				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
			} else {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
			}
		})
	}
}
