package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLivePrometheusSource(t *testing.T) {
	// Mock Prometheus endpoint returning text-exposition format.
	payload := `# HELP http_requests Total HTTP requests
# TYPE http_requests counter
http_requests{service="checkout",method="GET"} 1234 1700000000
http_requests{service="orders",method="POST"} 567 1700000000
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, "", 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	// The poll goroutine fetches immediately on start and then on every
	// interval; wait for the ingested events instead of sleeping a fixed
	// interval (a fixed sleep both slows the suite and flakes under load).
	waitFor(t, func() bool { return len(src.Events("")) >= 2 })

	// Verify events were ingested.
	events := src.Events("")
	if len(events) < 2 {
		t.Errorf("expected >=2 events, got %d", len(events))
	}
	if src.Name() != "prometheus" {
		t.Errorf("Name = %q, want 'prometheus'", src.Name())
	}
}

func TestLivePrometheusSourceEmptyURL(t *testing.T) {
	if src := NewLivePrometheusSource("", "tok", time.Second); src != nil {
		t.Error("empty URL should return nil")
	}
}

func TestLiveOtelSource(t *testing.T) {
	payload := `{"logs":[{"service":"checkout","timestamp":"2024-01-01T00:00:00Z","severity":"error","body":"nil pointer","attributes":{"file":"main.go"}}],"traces":[],"metrics":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	src := NewLiveOtelSource(srv.URL, "", 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	waitFor(t, func() bool { return len(src.Events("")) >= 1 })

	events := src.Events("")
	if len(events) == 0 {
		t.Error("expected at least 1 event from OTel source")
	}
	if src.Name() != "otel" {
		t.Errorf("Name = %q, want 'otel'", src.Name())
	}
}

func TestLiveOtelSourceEmptyURL(t *testing.T) {
	if src := NewLiveOtelSource("", "tok", time.Second); src != nil {
		t.Error("empty URL should return nil")
	}
}

func TestLiveKubernetesSource(t *testing.T) {
	depPayload := `{"items":[{"metadata":{"name":"checkout-deploy","creationTimestamp":"2024-01-01T00:00:00Z"},"spec":{"template":{"metadata":{"labels":{"app":"checkout"}}}}}]}`
	evPayload := `{"items":[{"metadata":{"namespace":"default","creationTimestamp":"2024-01-01T00:01:00Z"},"involvedObject":{"name":"checkout"},"type":"Warning","reason":"BackOff","message":"Back-off restarting failed container"}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "deployments") {
			_, _ = w.Write([]byte(depPayload))
			return
		}
		_, _ = w.Write([]byte(evPayload))
	}))
	defer srv.Close()

	src := NewLiveKubernetesSource(srv.URL, "test-token", "default", 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	waitFor(t, func() bool {
		return len(src.Events("")) >= 1 && len(src.Deployments("")) >= 1
	})

	events := src.Events("")
	if len(events) == 0 {
		t.Error("expected at least 1 event from K8s source")
	}
	deps := src.Deployments("")
	if len(deps) == 0 {
		t.Error("expected at least 1 deployment from K8s source")
	}
	if src.Name() != "kubernetes" {
		t.Errorf("Name = %q, want 'kubernetes'", src.Name())
	}
}

func TestLiveKubernetesSourceEmptyURL(t *testing.T) {
	if src := NewLiveKubernetesSource("", "token", "", time.Second); src != nil {
		t.Error("empty URL should return nil")
	}
}

func TestLiveSourceCloseStopsPolling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, "", 50*time.Millisecond)
	src.Close() // should not block, should stop the goroutine

	// Verify the goroutine exited by checking that Close returned.
	// (If it hung, the test would time out.)
}

// TestLivePrometheusSourceBearerAuth pins the env-only credential path: a
// non-empty token must arrive as a Bearer Authorization header on every poll
// (LoadSource reads it from KERN_PROMETHEUS_TOKEN — env only, never a
// config-file key).
func TestLivePrometheusSourceBearerAuth(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("# empty exposition\n"))
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, "sekrit", 50*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	waitFor(t, func() bool { return gotAuth.Load() != nil })
	if gotAuth.Load().(string) != "Bearer sekrit" {
		t.Errorf("Authorization = %q, want %q", gotAuth.Load(), "Bearer sekrit")
	}
}

// TestLiveOtelSourceBearerAuth is the OTLP-collector equivalent.
func TestLiveOtelSourceBearerAuth(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[],"traces":[],"metrics":[]}`))
	}))
	defer srv.Close()

	src := NewLiveOtelSource(srv.URL, "tok-otel", 50*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	waitFor(t, func() bool { return gotAuth.Load() != nil })
	if gotAuth.Load().(string) != "Bearer tok-otel" {
		t.Errorf("Authorization = %q, want %q", gotAuth.Load(), "Bearer tok-otel")
	}
}

// TestLiveSourceFetchErrors pins transport-failure visibility: an endpoint
// returning an error status must be countable (FetchErrors) and must surface
// in the canonical status snapshot — an unreachable endpoint may never read
// as healthy wiring with zero events.
func TestLiveSourceFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, "", 50*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	waitFor(t, func() bool {
		n, _ := src.FetchErrors()
		return n >= 1
	})
	n, last := src.FetchErrors()
	if n < 1 {
		t.Fatalf("FetchErrors = %d, want >= 1", n)
	}
	if !strings.Contains(last, "502") {
		t.Errorf("last error = %q, want it to name the status", last)
	}

	snap := StatusSnapshot(src)
	if got, _ := snap["fetch_errors"].(int); got != n {
		t.Errorf("StatusSnapshot fetch_errors = %v, want %d", snap["fetch_errors"], n)
	}
}

// waitFor polls cond until it returns true or the timeout elapses. Live
// sources ingest on their own poll goroutine, so tests wait on the ingested
// state rather than sleeping a fixed interval.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
