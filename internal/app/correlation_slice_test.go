package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestControlledDeploymentTraceableToTaskPR is the exit gate: a
// controlled deployment must be traceable back to Task/PR/commit/symbol
// through the app layer. The runtime source encodes a deployment of a commit
// whose message references PR #123, and error events carry task/agent/symbol
// attributes; TaskService.Correlate must resolve the full canonical chain
// (13.1) alert → service → deployment → commit → PR → task → agent → symbol,
// with trace links back to raw telemetry.
func TestControlledDeploymentTraceableToTaskPR(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module tracefixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// CacheService is a caching service.\nfunc CacheService() string { return \"c\" }\n")

	now := time.Now().Truncate(time.Second)
	store := runtime.NewStore()
	store.AddDeployment(domain.Deployment{
		Service:    "cache-svc",
		Version:    "v2.0.0",
		CommitSHA:  "abc123def",
		DeployedAt: now.Add(-10 * time.Minute),
	})
	// The commit references PR #123 → the chain must resolve a pr hop.
	store.AddCommit(runtime.Commit{
		SHA:         "abc123def",
		Message:     "fix cache eviction (#123)",
		Author:      "alice",
		Files:       []string{"service/cache.go"},
		CommittedAt: now.Add(-11 * time.Minute),
	})
	// Error events carry the task/agent/symbol hops + a trace ID (the 13.1
	// Event → Trace hop).
	store.IngestAll([]runtime.Event{
		{ID: "tr-err-1", Type: runtime.EventError, Service: "cache-svc", Severity: "critical",
			Message: "cache eviction panic", Timestamp: now.Add(-time.Minute), TraceID: "trace-abc",
			Attributes: map[string]string{
				"file": "service/cache.go", "symbol": "CacheService.Get",
				"task": "TASK-9", "agent": "agent-b",
			}},
	})

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.WithRuntimeSource(store)
	ts := NewTaskService(p, nil).WithAgentID("test")

	alert := domain.Alert{
		ID:         "alert-trace",
		Severity:   domain.SeverityCritical,
		Message:    "cache eviction 500s",
		Service:    "cache-svc",
		OccurredAt: now,
	}
	task, chain, text, err := ts.Correlate(alert)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Errorf("state = %s, want COMPLETED", task.State)
	}
	if text == "" {
		t.Error("correlate returned empty text")
	}

	// Exit gate: the controlled deployment traces back to PR/task/agent/symbol
	// (plus the service/deployment/commit hops that anchor it).
	byStage := map[string]map[string]bool{}
	for _, l := range chain.Links {
		if byStage[l.Stage] == nil {
			byStage[l.Stage] = map[string]bool{}
		}
		byStage[l.Stage][l.ID] = true
	}
	want := map[string]string{
		"service":    "cache-svc",
		"deployment": "v2.0.0",
		"commit":     "abc123de",
		"pr":         "123",
		"task":       "TASK-9",
		"agent":      "agent-b",
		"symbol":     "CacheService.Get",
	}
	for stage, id := range want {
		if !byStage[stage][id] {
			t.Errorf("chain missing %s hop %q (got %v)", stage, id, keysOf(byStage[stage]))
		}
	}

	// 13.1: the chain is traceable back to raw telemetry (trace links).
	if len(chain.TraceLinks) == 0 {
		t.Error("chain carries no trace links back to raw telemetry (13.1)")
	}
}

// TestSharedCorrelatorConsistentAcrossLanes verifies 13.3: correlate,
// investigate, and remediate lanes all reason over the SAME shared correlation
// service (same runtime source + lookback window) — a single source of truth
// for runtime evidence.
func TestSharedCorrelatorConsistentAcrossLanes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module sharedfixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	now := time.Now().Truncate(time.Second)
	store := runtime.NewStore()
	store.AddDeployment(domain.Deployment{Service: "svc", Version: "v1", CommitSHA: "abcd1234", DeployedAt: now.Add(-5 * time.Minute)})
	store.AddCommit(runtime.Commit{SHA: "abcd1234", Message: "change", Files: []string{"svc/main.go"}, CommittedAt: now.Add(-6 * time.Minute)})
	store.IngestAll([]runtime.Event{
		{ID: "e1", Type: runtime.EventError, Service: "svc", Severity: "critical", Message: "boom", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{"file": "svc/main.go"}},
	})

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.WithRuntimeSource(store)
	ts := NewTaskService(p, nil).WithAgentID("test")
	alert := domain.Alert{ID: "a1", Severity: domain.SeverityCritical, Message: "boom", Service: "svc", OccurredAt: now}

	// The correlate lane and the investigate lane must resolve the same
	// affected service from the SAME shared service.
	_, chain, _, err := ts.Correlate(alert)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if chain.Service != "svc" {
		t.Errorf("correlate service = %q, want svc", chain.Service)
	}
	_, inc, _, err := ts.InvestigateIncident(alert)
	if err != nil {
		t.Fatalf("InvestigateIncident: %v", err)
	}
	if inc == nil || inc.AffectedService != "svc" {
		t.Errorf("investigate affected service = %v, want svc", inc)
	}
}
