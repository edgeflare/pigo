package handlers

import (
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func GetMyPgRoleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// retrive the role from the request context, set by the middleware
		ctxRole := r.Context().Value(httputil.OIDCRoleClaimCtxKey)

		// retrieve the connection from request context
		conn, ok := r.Context().Value(httputil.PgConnCtxKey).(*pgxpool.Conn)
		if !ok || conn == nil {
			http.Error(w, "Failed to get connection from context", http.StatusInternalServerError)
			return
		}
		defer conn.Release()

		// query the current role using the connection
		var queryRole string
		err := conn.Conn().QueryRow(r.Context(), "SELECT current_role").Scan(&queryRole)
		if err != nil {
			http.Error(w, "Failed to query current role", http.StatusInternalServerError)
			return
		}

		// construct both roles as a json response
		roles := map[string]string{
			"ctx_role":   ctxRole.(string),
			"query_role": queryRole,
		}
		httputil.JSON(w, http.StatusOK, roles)
	}
}
