package webhook

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestAddRejectsUnsupportedScheme(t *testing.T) {
	c := New()
	if err := c.Add("bad", "ftp://example.com/hook"); err == nil {
		t.Fatal("expected error for ftp scheme, got nil")
	}
	if err := c.Add("bad", "not-a-url"); err == nil {
		t.Fatal("expected error for malformed url, got nil")
	}
	if n := len(c.URLs()); n != 0 {
		t.Fatalf("URLs = %d, want 0", n)
	}
}

func TestAddRejectsRestrictedHosts(t *testing.T) {
	c := New()
	restricted := []string{
		"http://127.0.0.1:8080/hook",
		"http://localhost/hook",
		"http://[::1]:8080/hook",
		"http://10.0.0.5/hook",
		"http://172.16.0.1/hook",
		"http://192.168.1.10/hook",
		"http://169.254.169.254/latest/meta-data",
	}
	for _, u := range restricted {
		if err := c.Add("hook", u); err == nil {
			t.Errorf("Add(%q) = nil, want error for restricted host", u)
		}
	}
	if n := len(c.URLs()); n != 0 {
		t.Fatalf("URLs = %d, want 0 (no restricted hook may be registered)", n)
	}
	// Public addresses remain acceptable.
	if err := c.Add("ok", "https://hooks.example.com/sre"); err != nil {
		t.Fatalf("Add(public) = %v, want nil", err)
	}
}

func TestAddRejectsDuplicateName(t *testing.T) {
	c := New()
	if err := c.Add("hook", "https://example.com/a"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := c.Add("hook", "https://example.com/b"); err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
	if n := len(c.URLs()); n != 1 {
		t.Fatalf("URLs = %d, want 1", n)
	}
}

func TestRemoveAndURLs(t *testing.T) {
	c := New()
	c.Add("b", "https://b.example/x")
	c.Add("a", "https://a.example/y")
	c.Add("c", "https://c.example/z")
	got := c.URLs()
	if len(got) != 3 {
		t.Fatalf("URLs len = %d, want 3", len(got))
	}
	if !strings.HasPrefix(got[0], "https://a.example") {
		t.Fatalf("first URL = %q, want sorted a first", got[0])
	}
	c.Remove("a")
	if n := len(c.URLs()); n != 2 {
		t.Fatalf("URLs after remove = %d, want 2", n)
	}
	// Removing an unknown name is a no-op.
	c.Remove("nope")
	if n := len(c.URLs()); n != 2 {
		t.Fatalf("URLs after no-op remove = %d, want 2", n)
	}
}

// echoHook starts an httptest server that records the last event body and
// returns 200.
func echoServer(t *testing.T) (*httptest.Server, *[]byte, *int) {
	t.Helper()
	var mu sync.Mutex
	var body []byte
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		body = b
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &hits
}

func TestDeliverSucceeds(t *testing.T) {
	srv, body, hits := echoServer(t)
	c := New()
	// Register the hook directly: httptest servers bind to loopback, which
	// Add() rejects as an SSRF guard. This test exercises Deliver mechanics.
	c.mu.Lock()
	c.hooks["hook"] = srv.URL
	c.mu.Unlock()
	errs := c.Deliver(eventbus.Event{
		ID:         "e-1",
		Kind:       eventbus.IncidentCreated,
		Source:     "web",
		Subject:    "inc-9",
		Service:    "checkout",
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if len(errs) != 0 {
		t.Fatalf("Deliver returned errors: %v", errs)
	}
	if *hits != 1 {
		t.Fatalf("hits = %d, want 1", *hits)
	}
	payload := string(*body)
	for _, want := range []string{`"id":"e-1"`, `"kind":"incident.created"`, `"subject":"inc-9"`, `"service":"checkout"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %q missing %q", payload, want)
		}
	}
}

func TestDeliverReportsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New()
	c.mu.Lock()
	c.hooks["hook"] = srv.URL
	c.mu.Unlock()
	errs := c.Deliver(eventbus.Event{ID: "e-1", Kind: eventbus.ApprovalGranted})
	if len(errs) != 1 {
		t.Fatalf("errs = %d, want 1", len(errs))
	}
	if !strings.Contains(errs[srv.URL].Error(), "status 500") {
		t.Fatalf("err = %v, want to mention status 500", errs[srv.URL])
	}
}

func TestDeliverDeadURLDoesNotPanic(t *testing.T) {
	// Deliberately nothing listening on this port.
	c := New()
	c.mu.Lock()
	c.hooks["dead"] = "http://127.0.0.1:1/hook"
	c.mu.Unlock()
	c.SetTimeout(100 * time.Millisecond)
	errs := c.Deliver(eventbus.Event{ID: "e", Kind: eventbus.IncidentCreated})
	if len(errs) != 1 {
		t.Fatalf("errs = %d, want 1 (dead URL must be reported, not panic)", len(errs))
	}
}

func TestDeliverNoHooks(t *testing.T) {
	c := New()
	errs := c.Deliver(eventbus.Event{ID: "e", Kind: eventbus.IncidentCreated})
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want empty", errs)
	}
}

func ExampleClient_Deliver() {
	c := New()
	_ = c.Add("sre", "https://hooks.example.com/sre")
	fmt.Println(len(c.URLs()))
	// Output: 1
}
