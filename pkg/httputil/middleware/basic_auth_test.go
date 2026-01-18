package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/edgeflare/pigo/pkg/httputil"
)

func TestVerifyBasicAuth(t *testing.T) {
	tests := []struct {
		config         *BasicAuthConfig
		name           string
		authHeader     string
		expectedBody   string
		expectedUser   string
		expectedStatus int
	}{
		{
			name:           "missing authorization header",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Authorization header missing\n",
		},
		{
			name:           "invalid authorization format",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "Bearer some-token",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Invalid authorization format\n",
		},
		{
			name:           "invalid base64 encoding",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "Basic invalid-base64",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Invalid base64 encoding\n",
		},
		{
			name:           "invalid credentials format",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("userpass")),
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Invalid credentials format\n",
		},
		{
			name:           "invalid credentials",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("user:wrongpass")),
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Invalid credentials\n",
		},
		{
			name:           "valid credentials",
			config:         BasicAuthCreds(map[string]string{"user": "pass"}),
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass")),
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
			expectedUser:   "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			handler := VerifyBasicAuth(tt.config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, ok := r.Context().Value(httputil.BasicAuthCtxKey).(string)
				if !ok || user != tt.expectedUser {
					http.Error(w, "User not found in context", http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			}))

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("status code: expected %v, got %v", tt.expectedStatus, status)
			}

			if body := rr.Body.String(); body != tt.expectedBody {
				t.Errorf("body: expected %v, got %v", tt.expectedBody, body)
			}
		})
	}
}
