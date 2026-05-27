package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSWithOptions(t *testing.T) {
	tests := []struct {
		name            string
		options         *CORSOptions
		method          string
		origin          string // empty = non-CORS request
		expectedHeaders map[string]string
		absentHeaders   []string // headers that must NOT be present
		expectedStatus  int
	}{
		{
			name:    "non-CORS request passes through untouched",
			method:  http.MethodGet,
			origin:  "", // no Origin header
			options: defaultCORSOptions(),
			absentHeaders: []string{
				"Access-Control-Allow-Origin",
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:    "wildcard origin — actual request",
			method:  http.MethodGet,
			origin:  "http://anything.example.com",
			options: defaultCORSOptions(),
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":   "*",
				"Access-Control-Expose-Headers": "Content-Range, Prefer",
			},
			// Allow-Methods and Allow-Headers must NOT appear on actual requests,
			// only on preflights.
			absentHeaders: []string{
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
				"Access-Control-Allow-Credentials", // false in default; must be absent
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:    "wildcard origin — preflight",
			method:  http.MethodOptions,
			origin:  "http://anything.example.com",
			options: defaultCORSOptions(),
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, Accept, Origin, Cache-Control, X-Requested-With",
				"Access-Control-Max-Age":       "86400",
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "specific origin reflected — actual request",
			method: http.MethodGet,
			origin: "http://example.com",
			options: &CORSOptions{
				AllowedOrigins:   []string{"http://example.com", "http://other.com"},
				AllowedMethods:   []string{"GET", "POST"},
				AllowedHeaders:   []string{"Content-Type"},
				AllowCredentials: true,
			},
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":      "http://example.com",
				"Access-Control-Allow-Credentials": "true",
				"Vary":                             "Origin",
			},
			absentHeaders: []string{
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "specific origin reflected — preflight",
			method: http.MethodOptions,
			origin: "http://example.com",
			options: &CORSOptions{
				AllowedOrigins:   []string{"http://example.com"},
				AllowedMethods:   []string{"GET", "POST"},
				AllowedHeaders:   []string{"Content-Type"},
				AllowCredentials: true,
				MaxAge:           3600,
			},
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":      "http://example.com",
				"Access-Control-Allow-Methods":     "GET, POST",
				"Access-Control-Allow-Headers":     "Content-Type",
				"Access-Control-Allow-Credentials": "true",
				"Access-Control-Max-Age":           "3600",
				"Vary":                             "Origin",
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "disallowed origin — no CORS headers set",
			method: http.MethodGet,
			origin: "http://evil.com",
			options: &CORSOptions{
				AllowedOrigins: []string{"http://example.com"},
				AllowedMethods: []string{"GET"},
			},
			absentHeaders: []string{
				"Access-Control-Allow-Origin",
				"Access-Control-Allow-Methods",
			},
			expectedStatus: http.StatusOK, // pass-through; browser enforces the block
		},
		{
			name:    "nil options falls back to defaults",
			method:  http.MethodGet,
			origin:  "http://anything.com",
			options: nil,
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin": "*",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "empty options — no CORS headers",
			method:         http.MethodGet,
			origin:         "http://example.com",
			options:        &CORSOptions{},
			absentHeaders:  []string{"Access-Control-Allow-Origin"},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com/api/test", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			rr := httptest.NewRecorder()
			handler := CORSWithOptions(tt.options)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("status: expected %d, got %d", tt.expectedStatus, status)
			}

			for header, want := range tt.expectedHeaders {
				if got := rr.Header().Get(header); got != want {
					t.Errorf("header %q: expected %q, got %q", header, want, got)
				}
			}

			for _, header := range tt.absentHeaders {
				if got := rr.Header().Get(header); got != "" {
					t.Errorf("header %q should be absent, got %q", header, got)
				}
			}
		})
	}
}

func TestCORSWildcardWithCredentialsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wildcard origin + AllowCredentials, got none")
		}
	}()
	CORSWithOptions(&CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}
