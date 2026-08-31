package kernsdk

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/web"
)

// sdkFixture writes a tiny Go module so the web console (backed by the same
// TaskService application services the CLI/MCP use) can analyze it.
func sdkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sdkserver\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\n// Greet says hello.\nfunc Greet() string { return \"hi\" }\n\nfunc main() { _ = Greet() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGoSDKAgainstControlPlane is the exit gate: the Go SDK drives
// the SAME control-plane application services as the CLI and MCP — through the
// same REST surface the Python/TypeScript SDKs use. A real kern-server
// (internal/web over a fixture) backs the client.
func TestGoSDKAgainstControlPlane(t *testing.T) {
	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	ctx := context.Background()

	// Health.
	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h == nil {
		t.Error("Health returned nil")
	}

	// Analyze → context packet (same services as kern analyze / kern_analyze).
	an, err := c.Analyze(ctx, "Greet")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if an == nil {
		t.Error("Analyze returned nil")
	}

	// Plan → implementation plan.
	pl, err := c.Plan(ctx, "Greet")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if pl == nil {
		t.Error("Plan returned nil")
	}

	// What-if → impact simulation.
	wi, err := c.WhatIf(ctx, "remove_symbol", "Greet", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if wi == nil {
		t.Error("WhatIf returned nil")
	}

	// Memory add + list.
	if _, err := c.MemoryAdd(ctx, "go sdk lesson", "lesson", "test", []string{"sdk"}); err != nil {
		t.Fatalf("MemoryAdd: %v", err)
	}
	ms, err := c.MemoryList(ctx)
	if err != nil {
		t.Fatalf("MemoryList: %v", err)
	}
	items, _ := ms["items"].([]any)
	if len(items) == 0 {
		t.Errorf("MemoryList returned no memories after MemoryAdd: %v", ms)
	}

	// Task submit + fetch.
	sub, err := c.TaskSubmit(ctx, "Greet", "code")
	if err != nil {
		t.Fatalf("TaskSubmit: %v", err)
	}
	if sub == nil {
		t.Error("TaskSubmit returned nil")
	}

	// Agents roster.
	agents, err := c.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	specs, _ := agents["specialists"].([]any)
	if len(specs) == 0 {
		t.Errorf("Agents returned empty roster: %v", agents)
	}
}

// TestGoSDKReturnsStatusErrors verifies the SDK surfaces non-2xx as errors.
func TestGoSDKReturnsStatusErrors(t *testing.T) {
	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	// A task that does not exist must yield an error, not a silent nil.
	_, err = c.Task(context.Background(), "task-does-not-exist")
	if err == nil {
		t.Fatal("Task(nonexistent) should error")
	}
}
