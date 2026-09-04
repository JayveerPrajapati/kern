package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestV1EventsStreamSSE(t *testing.T) {
	root := t.TempDir()

	app, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	server := httptest.NewServer(app)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Publish an event onto the bus
	go func() {
		time.Sleep(50 * time.Millisecond)
		app.Bus().Publish(eventbus.Event{
			Kind:    eventbus.GateEvaluated,
			Subject: "task-test-sse",
			Payload: map[string]string{"gate": "G2_BOUNDARIES", "status": "PASS"},
		})
	}()

	scanner := bufio.NewScanner(resp.Body)
	foundConnected := false
	foundGateEvent := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "event: connected") {
			foundConnected = true
		}
		if strings.Contains(line, "gate.evaluated") {
			foundGateEvent = true
			break
		}
	}

	if !foundConnected {
		t.Errorf("expected connected event in stream")
	}
	if !foundGateEvent {
		t.Errorf("expected gate.evaluated event in stream")
	}
}
