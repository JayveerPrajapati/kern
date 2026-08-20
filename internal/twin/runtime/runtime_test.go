package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

func TestBuildFromRuntimeStore(t *testing.T) {
	store := runtime.NewStore()
	store.AddDeployment(domain.Deployment{Service: "api", Version: "v1.2", DeployedAt: time.Now()})
	store.Ingest(runtime.Event{Service: "api", Type: runtime.EventError, Timestamp: time.Now(), Attributes: map[string]string{"file": "main.go"}})

	b := New(store)
	nodes, edges, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Should have: 1 deployment, 1 error, 1 health node = 3 nodes
	if len(nodes) < 2 {
		t.Fatalf("nodes = %d, want at least 2", len(nodes))
	}
	// Should have a caused_by edge linking error to file
	hasCausedBy := false
	for _, e := range edges {
		if e.Kind == "caused_by" {
			hasCausedBy = true
		}
	}
	if !hasCausedBy {
		t.Error("no caused_by edges")
	}
}

func TestBuildHealthStatus(t *testing.T) {
	store := runtime.NewStore()
	// Add 15 error events for a service
	for i := 0; i < 15; i++ {
		store.Ingest(runtime.Event{Service: "api", Type: runtime.EventError, Timestamp: time.Now()})
	}
	b := New(store)
	nodes, _, _ := b.Build()
	for _, n := range nodes {
		if n.Kind == "service-health" && strings.Contains(n.Label, "unhealthy") {
			return // found unhealthy
		}
	}
	t.Error("expected unhealthy service-health node")
}
