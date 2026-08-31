package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	// Wait for at least one poll.
	time.Sleep(300 * time.Millisecond)

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
	if src := NewLivePrometheusSource("", time.Second); src != nil {
		t.Error("empty URL should return nil")
	}
}

func TestLiveOtelSource(t *testing.T) {
	payload := `{"logs":[{"service":"checkout","timestamp":"2024-01-01T00:00:00Z","severity":"error","body":"nil pointer","attributes":{"file":"main.go"}}],"traces":[],"metrics":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	src := NewLiveOtelSource(srv.URL, 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	time.Sleep(300 * time.Millisecond)

	events := src.Events("")
	if len(events) == 0 {
		t.Error("expected at least 1 event from OTel source")
	}
	if src.Name() != "otel" {
		t.Errorf("Name = %q, want 'otel'", src.Name())
	}
}

func TestLiveOtelSourceEmptyURL(t *testing.T) {
	if src := NewLiveOtelSource("", time.Second); src != nil {
		t.Error("empty URL should return nil")
	}
}

func TestLiveKubernetesSource(t *testing.T) {
	depPayload := `{"items":[{"metadata":{"name":"checkout-deploy","creationTimestamp":"2024-01-01T00:00:00Z"},"spec":{"template":{"metadata":{"labels":{"app":"checkout"}}}}}]}`
	evPayload := `{"items":[{"metadata":{"namespace":"default","creationTimestamp":"2024-01-01T00:01:00Z"},"involvedObject":{"name":"checkout"},"type":"Warning","reason":"BackOff","message":"Back-off restarting failed container"}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "deployments") {
			w.Write([]byte(depPayload))
			return
		}
		w.Write([]byte(evPayload))
	}))
	defer srv.Close()

	src := NewLiveKubernetesSource(srv.URL, "test-token", "default", 100*time.Millisecond)
	if src == nil {
		t.Fatal("expected non-nil source")
	}
	defer src.Close()

	time.Sleep(300 * time.Millisecond)

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
		w.Write([]byte(""))
	}))
	defer srv.Close()

	src := NewLivePrometheusSource(srv.URL, 50*time.Millisecond)
	src.Close() // should not block, should stop the goroutine

	// Verify the goroutine exited by checking that Close returned.
	// (If it hung, the test would time out.)
}
