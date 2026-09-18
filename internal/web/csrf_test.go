package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// csrfTestApp builds a minimal App with a mux and no limiter for the
// cross-origin guard tests.
func csrfTestApp(t *testing.T) *App {
	t.Helper()
	app := &App{
		mux:         http.NewServeMux(),
		rateLimiter: nil,
	}
	app.mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return app
}

func doReq(t *testing.T, app *App, method, origin, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/echo", strings.NewReader("{}"))
	req.RemoteAddr = "10.0.0.9:1"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestCsrfNoOriginPasses(t *testing.T) {
	// curl/SDK/bearer clients send no Origin: never rejected.
	app := csrfTestApp(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if rec := doReq(t, app, m, "", "127.0.0.1:8090"); rec.Code != http.StatusOK {
			t.Errorf("%s without Origin: code %d, want 200", m, rec.Code)
		}
	}
}

func TestCsrfSameOriginPasses(t *testing.T) {
	app := csrfTestApp(t)
	// Browser same-origin POST: Origin host matches Host (browsers always
	// echo the actual port in Origin).
	rec := doReq(t, app, http.MethodPost, "http://127.0.0.1:8090", "127.0.0.1:8090")
	if rec.Code != http.StatusOK {
		t.Errorf("same-origin POST: code %d, want 200; body=%s", rec.Code, rec.Body)
	}
	// Hostname form with the port echoed.
	rec = doReq(t, app, http.MethodPost, "http://localhost:8090", "localhost:8090")
	if rec.Code != http.StatusOK {
		t.Errorf("same-origin localhost POST: code %d, want 200", rec.Code)
	}
}

func TestCsrfCrossOriginRejected(t *testing.T) {
	app := csrfTestApp(t)
	// Malicious page at evil.example POSTs to the console: Origin host
	// differs from the loopback Host.
	rec := doReq(t, app, http.MethodPost, "http://evil.example", "127.0.0.1:8090")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: code %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Origin") {
		t.Errorf("403 body should name Origin: %s", rec.Body.String())
	}
}

func TestCsrfDnsRebindingRejected(t *testing.T) {
	app := csrfTestApp(t)
	// DNS rebinding: attacker.com resolves to 127.0.0.1, so Host AND Origin
	// both name attacker.com (they match each other). The Host allowlist
	// must catch it.
	rec := doReq(t, app, http.MethodPost, "http://attacker.com", "attacker.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("rebinding POST: code %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Host") {
		t.Errorf("403 body should name Host guard: %s", rec.Body.String())
	}
}

func TestCsrfReadsNotChecked(t *testing.T) {
	app := csrfTestApp(t)
	// GETs are never rejected even with a foreign Origin: the dashboard
	// must remain readable cross-origin (SSE clients etc).
	rec := doReq(t, app, http.MethodGet, "http://evil.example", "127.0.0.1:8090")
	if rec.Code != http.StatusOK {
		t.Errorf("GET with foreign Origin: code %d, want 200 (reads never blocked)", rec.Code)
	}
}

func TestCsrfAllowedHostsEnv(t *testing.T) {
	app := csrfTestApp(t)
	// Reverse-proxy deployment: proxy hostname in the allowlist makes a
	// browser-initiated POST with that Host pass.
	t.Setenv(allowedHostsEnv, "console.example")
	rec := doReq(t, app, http.MethodPost, "http://console.example", "console.example")
	if rec.Code != http.StatusOK {
		t.Errorf("POST from allowlisted proxy host: code %d, want 200; body=%s", rec.Code, rec.Body)
	}
	// Without the allowlist entry the same request is rejected.
	t.Setenv(allowedHostsEnv, "")
	rec = doReq(t, app, http.MethodPost, "http://console.example", "console.example")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST from non-allowlisted host: code %d, want 403", rec.Code)
	}
}

func TestHostMatches(t *testing.T) {
	cases := []struct {
		origin, req string
		want        bool
	}{
		{"http://127.0.0.1:8090", "127.0.0.1:8090", true},
		{"http://127.0.0.1", "127.0.0.1:80", true},
		{"https://127.0.0.1", "127.0.0.1:443", true},
		{"http://localhost:8090", "localhost:8090", true},
		{"http://localhost", "localhost:80", true},
		// An Origin without a port is only same-origin with the standard
		// port — http://localhost (port 80) does NOT match :8090.
		{"http://localhost", "localhost:8090", false},
		{"http://evil.example", "127.0.0.1:8090", false},
		{"http://127.0.0.1:9999", "127.0.0.1:8090", false},
	}
	for _, c := range cases {
		if got := hostMatches(c.origin, c.req); got != c.want {
			t.Errorf("hostMatches(%q, %q) = %v, want %v", c.origin, c.req, got, c.want)
		}
	}
}
