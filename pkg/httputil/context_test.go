package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextKeys_NoCollision(t *testing.T) {
	// Two different keys with the same name string must not be equal.
	// This would silently collide if keys were plain strings.
	type foreignKey struct{ name string }
	foreign := &foreignKey{"OIDCUser"}

	ctx := context.WithValue(context.Background(), OIDCUserCtxKey, map[string]any{"sub": "u1"})
	if v := ctx.Value(foreign); v != nil {
		t.Fatal("foreign key with same name string should not retrieve httputil value")
	}
}

func TestOIDCUser(t *testing.T) {
	want := map[string]any{"sub": "user-1", "email": "u@example.com"}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), OIDCUserCtxKey, want))

	got, ok := OIDCUser(req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got["sub"] != want["sub"] {
		t.Errorf("sub: got %v, want %v", got["sub"], want["sub"])
	}
}

func TestOIDCUser_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := OIDCUser(req)
	if ok {
		t.Fatal("expected ok=false when no claims in context")
	}
}

func TestOIDCUser_NilClaims(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), OIDCUserCtxKey, (map[string]any)(nil)))
	_, ok := OIDCUser(req)
	if ok {
		t.Fatal("expected ok=false for nil claims map")
	}
}

func TestBasicAuthUser(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), BasicAuthCtxKey, "alice"))

	user, ok := BasicAuthUser(req)
	if !ok || user != "alice" {
		t.Errorf("got (%q, %v), want (\"alice\", true)", user, ok)
	}
}

func TestBasicAuthUser_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := BasicAuthUser(req)
	if ok {
		t.Fatal("expected ok=false when no user in context")
	}
}

func TestBind(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	body := `{"name":"alice","age":30}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var got payload
	if err := Bind(req, &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "alice" || got.Age != 30 {
		t.Errorf("got %+v", got)
	}
}

func TestBind_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`not json`))
	var dst map[string]any
	if err := Bind(req, &dst); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBind_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(``))
	var dst map[string]any
	if err := Bind(req, &dst); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestBind_BodyTooLarge(t *testing.T) {
	// WithMaxBytes(10) and a body larger than that must return an error
	// mentioning the limit.
	big := strings.NewReader(`{"a":"` + strings.Repeat("x", 100) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/", big)

	var dst map[string]any
	err := Bind(req, &dst, WithMaxBytes(10))
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("error should mention the limit, got: %v", err)
	}
}

func TestBind_WithMaxBytes_Accepted(t *testing.T) {
	body := `{"k":"v"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var dst map[string]any
	if err := Bind(req, &dst, WithMaxBytes(1<<20)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJSON(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, http.StatusOK, map[string]string{"hello": "world"})

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d", res.StatusCode, http.StatusOK)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}

	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("body: got %v", got)
	}
}

func TestJSON_MarshalFailure(t *testing.T) {
	w := httptest.NewRecorder()
	// channels cannot be marshalled to JSON.
	JSON(w, http.StatusOK, make(chan int))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on marshal failure, got %d", w.Code)
	}
}

func TestJSON_StatusCodes(t *testing.T) {
	for _, code := range []int{http.StatusCreated, http.StatusAccepted, http.StatusNoContent} {
		w := httptest.NewRecorder()
		JSON(w, code, map[string]any{})
		if w.Code != code {
			t.Errorf("code %d: got %d", code, w.Code)
		}
	}
}

func TestText(t *testing.T) {
	w := httptest.NewRecorder()
	Text(w, http.StatusOK, "hello")

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type: got %q", ct)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	if buf.String() != "hello" {
		t.Errorf("body: got %q", buf.String())
	}
}

func TestHTML(t *testing.T) {
	w := httptest.NewRecorder()
	HTML(w, http.StatusOK, "<h1>hi</h1>")

	res := w.Result()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q", ct)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	if buf.String() != "<h1>hi</h1>" {
		t.Errorf("body: got %q", buf.String())
	}
}

func TestBlob(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	w := httptest.NewRecorder()
	Blob(w, http.StatusOK, "image/png", data)

	res := w.Result()
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: got %q", ct)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("body bytes mismatch")
	}
}

func TestError(t *testing.T) {
	w := httptest.NewRecorder()
	Error(w, http.StatusBadRequest, "bad input")

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}

	var got ErrorResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code != http.StatusBadRequest {
		t.Errorf("code: got %d", got.Code)
	}
	if got.Message != "bad input" {
		t.Errorf("message: got %q", got.Message)
	}
}

func TestError_CommonCodes(t *testing.T) {
	cases := []struct {
		code    int
		message string
	}{
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not found"},
		{http.StatusInternalServerError, "internal error"},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		Error(w, tc.code, tc.message)

		if w.Code != tc.code {
			t.Errorf("[%d] status: got %d", tc.code, w.Code)
		}

		var got ErrorResponse
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Errorf("[%d] decode: %v", tc.code, err)
			continue
		}
		if got.Message != tc.message {
			t.Errorf("[%d] message: got %q, want %q", tc.code, got.Message, tc.message)
		}
	}
}
