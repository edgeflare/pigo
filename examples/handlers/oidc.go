package handlers

import (
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
	// "github.com/zitadel/oidc/v3/pkg/oidc"
)

type AuthzResponse struct {
	Allowed bool `json:"allowed"`
}

// GetMyAccountHandler retrieves the user information from the OIDC context and responds with the user details.
//
// If the user is not found in the context or an error occurs, an HTTP 401 Unauthorized error is returned.
func GetMyAccountHandler(w http.ResponseWriter, r *http.Request) {
	var user map[string]any
	if user, ok := httputil.OIDCUser(r); !ok || user == nil {
		http.Error(w, "User not found in context", http.StatusUnauthorized)
		return
	}
	httputil.JSON(w, http.StatusOK, user)
}

// GetSimpleAuthzHandler performs an authorization check based on a requested /endpoint/{claim}/{value} path
// eg `GET /endpoint/editor/true` will check if the user has the claim `editor` with the value `true`
// The response is an `AuthzResponse` object indicating whether the user is authorized (Allowed: true) or not (Allowed: false)
func GetSimpleAuthzHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.OIDCUser(r)
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	requestedClaim := r.PathValue("claim")
	requestedValue := r.PathValue("value")
	if value, ok := user[requestedClaim].(string); !ok || value != requestedValue {
		httputil.JSON(w, http.StatusOK, AuthzResponse{Allowed: false})
		return
	}

	httputil.JSON(w, http.StatusOK, AuthzResponse{Allowed: true})
}
