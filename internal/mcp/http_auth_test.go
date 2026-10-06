package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithMCPAuth(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

	// Auth off (empty token): the handler is returned untouched.
	off := httptest.NewRecorder()
	withMCPAuth("", ok)(off, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if off.Code != http.StatusNoContent {
		t.Fatalf("auth off must pass through, got %d", off.Code)
	}

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic s3cret", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"prefix only", "Bearer ", http.StatusUnauthorized},
		{"correct token", "Bearer s3cret", http.StatusNoContent},
		{"scheme is case-insensitive", "bearer s3cret", http.StatusNoContent},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		withMCPAuth("s3cret", ok)(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: 401 must carry WWW-Authenticate", c.name)
		}
	}
}

func TestHTTPServerCoreProtectsMCPNotHealth(t *testing.T) {
	t.Setenv(mcpAuthTokenEnv, "s3cret")
	_, mux := newHTTPServerCore(t.Context())

	unauth := httptest.NewRecorder()
	mux.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("/mcp without a token must be 401 when %s is set, got %d", mcpAuthTokenEnv, unauth.Code)
	}

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code == http.StatusUnauthorized {
		t.Fatal("/health must stay open for liveness probes")
	}
}
