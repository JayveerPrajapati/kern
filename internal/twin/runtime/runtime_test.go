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

// TestBuildDeploymentNodeKind locks the deployment node kind attributes
// (Feature Batch E): each deployment node carries the Deployment attribute
// with the service/version/commit/deployed-at the runtime source provided.
func TestBuildDeploymentNodeKind(t *testing.T) {
	store := runtime.NewStore()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	store.AddDeployment(domain.Deployment{Service: "api", Version: "v1.2.3", CommitSHA: "abc1234", DeployedAt: at})
	b := New(store)
	nodes, edges, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one deployment node, carrying the attributes.
	found := 0
	for _, n := range nodes {
		if n.Kind != "deployment" {
			continue
		}
		found++
		if n.Deployment == nil {
			t.Fatalf("deployment node %s has nil Deployment attribute", n.ID)
		}
		if n.Deployment.Service != "api" || n.Deployment.Version != "v1.2.3" ||
			n.Deployment.CommitSHA != "abc1234" || !n.Deployment.DeployedAt.Equal(at) {
			t.Fatalf("deployment node attributes wrong: %+v", n.Deployment)
		}
	}
	if found != 1 {
		t.Fatalf("deployment nodes = %d, want 1", found)
	}
	// The deployment still links to its service node (existing edge contract).
	hasDeploys := false
	for _, e := range edges {
		if e.Kind == "deploys" {
			hasDeploys = true
		}
	}
	if !hasDeploys {
		t.Error("no deploys edges after Build")
	}
}

// TestBuildNoSourceZeroDeployments locks the zero-instance contract: with no
// runtime source wired the deployment kind is not invented — a nil builder
// source is not reachable here (Builder requires a source), so the invariant
// is pinned at the package level: an empty store yields no deployment nodes.
func TestBuildNoDeploymentsWithoutData(t *testing.T) {
	store := runtime.NewStore()
	b := New(store)
	nodes, _, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Kind == "deployment" {
			t.Fatalf("deployment node invented without deployment data: %+v", n)
		}
	}
}
