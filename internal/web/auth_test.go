package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWebAuthGateUnsetIsOpen (P2-10): with KERN_AUTH_TOKEN unset (the loopback
// default) the console serves without a token — the existing behavior.
func TestWebAuthGateUnsetIsOpen(t *testing.T) {
	t.Setenv("KERN_AUTH_TOKEN", "")
	app := newTestApp(t)
	if rec := get(t, app, "/api/overview"); rec.Code != http.StatusOK {
		t.Fatalf("no gate: /api/overview = %d, want 200", rec.Code)
	}
}

// TestWebAuthGate (P2-10): with KERN_AUTH_TOKEN set, every request must carry
// the matching bearer token — state-mutating + LLM endpoints are 401
// otherwise (fail closed, constant-time compare like enterprise mode).
func TestWebAuthGate(t *testing.T) {
	t.Setenv("KERN_AUTH_TOKEN", "s3cret-token")
	app := newTestApp(t)

	request := func(path, auth string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, path := range []string{"/api/overview", "/api/approvals/pending", "/v1/analyze"} {
		if code := request(path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", path, code)
		}
	}
	if code := request("/api/overview", "Bearer wrong-token"); code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", code)
	}
	if code := request("/api/overview", "Bearer s3cret-token"); code != http.StatusOK {
		t.Errorf("correct token = %d, want 200", code)
	}
	if code := request("/api/overview", "Basic dXNlcjpwYXNz"); code != http.StatusUnauthorized {
		t.Errorf("non-bearer scheme = %d, want 401", code)
	}
}
