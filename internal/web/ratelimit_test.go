package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinWindow(t *testing.T) {
	rl := newRateLimiter(3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		ok, retry := rl.allow("10.0.0.1", now)
		if !ok {
			t.Fatalf("request %d within limit: denied (retry %d)", i+1, retry)
		}
	}
	// The 4th request in the same window is denied with a retry >= 1s.
	ok, retry := rl.allow("10.0.0.1", now.Add(10*time.Second))
	if ok {
		t.Fatal("4th request in window: allowed, want denied")
	}
	if retry < 1 {
		t.Errorf("retry-after = %d, want >= 1", retry)
	}
}

func TestRateLimiterWindowRollsOver(t *testing.T) {
	rl := newRateLimiter(2)
	now := time.Now()
	rl.allow("10.0.0.2", now)
	rl.allow("10.0.0.2", now)
	if ok, _ := rl.allow("10.0.0.2", now.Add(30*time.Second)); ok {
		t.Fatal("3rd request before window rollover: allowed, want denied")
	}
	// After the window elapses the count resets.
	if ok, _ := rl.allow("10.0.0.2", now.Add(61*time.Second)); !ok {
		t.Fatal("request after window rollover: denied, want allowed")
	}
}

func TestRateLimiterPerIPIsolation(t *testing.T) {
	rl := newRateLimiter(1)
	now := time.Now()
	if ok, _ := rl.allow("10.0.0.1", now); !ok {
		t.Fatal("first request from A: denied")
	}
	// A different IP is unaffected.
	if ok, _ := rl.allow("10.0.0.2", now); !ok {
		t.Fatal("first request from B: denied (per-IP buckets must be isolated)")
	}
	if ok, _ := rl.allow("10.0.0.1", now); ok {
		t.Fatal("second request from A: allowed, want denied")
	}
}

func TestNewRateLimiterDisabled(t *testing.T) {
	if rl := newRateLimiter(0); rl != nil {
		t.Error("limit 0: want nil (disabled)")
	}
	if rl := newRateLimiter(-1); rl != nil {
		t.Error("negative limit: want nil (disabled)")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.5:4321"
	if got := clientIP(r); got != "192.168.1.5" {
		t.Errorf("clientIP = %q, want 192.168.1.5", got)
	}
	r.RemoteAddr = "bad-addr"
	if got := clientIP(r); got != "bad-addr" {
		t.Errorf("clientIP(malformed) = %q, want raw fallback", got)
	}
}

func TestMutatingMethod(t *testing.T) {
	for m, want := range map[string]bool{
		http.MethodPost:    true,
		http.MethodPut:     true,
		http.MethodPatch:   true,
		http.MethodDelete:  true,
		http.MethodGet:     false,
		http.MethodHead:    false,
		http.MethodOptions: false,
	} {
		if got := mutatingMethod(m); got != want {
			t.Errorf("mutatingMethod(%s) = %v, want %v", m, got, want)
		}
	}
}

// TestServeHTTPRateLimitsMutations drives the full ServeHTTP path: mutating
// requests beyond the cap get 429 with Retry-After; GETs are never
// throttled.
func TestServeHTTPRateLimitsMutations(t *testing.T) {
	// A tiny App that only needs the limiter and a mux; avoid building a
	// full New() fixture (index build) for the limiter path.
	app := &App{
		mux:         http.NewServeMux(),
		rateLimiter: newRateLimiter(2),
	}
	app.mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	do := func(method, addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/echo", strings.NewReader("{}"))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	// Two POSTs pass, the third is throttled.
	if rec := do(http.MethodPost, "10.0.0.9:1"); rec.Code != http.StatusOK {
		t.Fatalf("POST 1: code %d, want 200", rec.Code)
	}
	if rec := do(http.MethodPost, "10.0.0.9:1"); rec.Code != http.StatusOK {
		t.Fatalf("POST 2: code %d, want 200", rec.Code)
	}
	rec := do(http.MethodPost, "10.0.0.9:1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("POST 3: code %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}

	// Different IP is unaffected.
	if rec := do(http.MethodPost, "10.0.0.10:1"); rec.Code != http.StatusOK {
		t.Fatalf("POST from second IP: code %d, want 200", rec.Code)
	}

	// GETs are never throttled, even past the cap.
	for i := 0; i < 5; i++ {
		if rec := do(http.MethodGet, "10.0.0.9:1"); rec.Code != http.StatusOK {
			t.Fatalf("GET %d: code %d, want 200 (reads never throttled)", i+1, rec.Code)
		}
	}
}

// TestServeHTTPRateLimitDisabled: with a nil limiter nothing is throttled.
func TestServeHTTPRateLimitDisabled(t *testing.T) {
	app := &App{
		mux:         http.NewServeMux(),
		rateLimiter: nil,
	}
	app.mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("{}"))
		req.RemoteAddr = "10.0.0.9:1"
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d with disabled limiter: code %d, want 200", i+1, rec.Code)
		}
	}
}
