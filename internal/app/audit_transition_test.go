package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

func TestTaskTransitionAudited(t *testing.T) {
	root := auditTestRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("audit-agent")
	if ts.auditLog == nil {
		t.Fatal("NewTaskService must default the audit log to the platform's unified chain")
	}

	task, err := ts.Create("audit transition test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ts.transition(task, domain.TaskExecuting); err != nil {
		t.Fatalf("transition: %v", err)
	}

	// The entry must be retrievable from the persisted store (what the CLI /
	// MCP audit tools read), not just the in-memory log.
	entries, err := ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	var found *governance.AuditEntry
	for i := range entries {
		if entries[i].Action == "task.transition" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no task.transition audit entry for task %s; entries: %+v", task.ID, entries)
	}
	if found.TaskID != task.ID {
		t.Errorf("entry TaskID = %q, want %q", found.TaskID, task.ID)
	}
	if found.AgentID != "audit-agent" {
		t.Errorf("entry AgentID = %q, want audit-agent", found.AgentID)
	}
	if !strings.Contains(found.Reason, string(domain.TaskCreated)) || !strings.Contains(found.Reason, string(domain.TaskExecuting)) {
		t.Errorf("entry Reason should record %s -> %s, got %q", domain.TaskCreated, domain.TaskExecuting, found.Reason)
	}
	if found.Timestamp.IsZero() {
		t.Error("entry Timestamp must be set")
	}
	if found.Hash == "" {
		t.Error("entry must be hash-chained into the tamper-evident chain")
	}
}

func TestTaskTransitionAuditFailureDoesNotAbort(t *testing.T) {
	root := auditTestRoot(t)
	ts := NewTaskService(&Platform{root: root}, nil).WithAgentID("audit-agent")
	ts.WithAuditLog(governance.NewAuditLog().WithStore(failingAuditStore{}))

	task, err := ts.Create("audit failure test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Capture the default logger (log.Printf in auditTransition) — the log
	// package caches its writer at init, so os.Stderr swapping would miss it.
	var logBuf strings.Builder
	old := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(old)
	if err := ts.transition(task, domain.TaskExecuting); err != nil {
		t.Fatalf("audit-write failure must not abort the transition, got: %v", err)
	}

	// The transition itself must have happened.
	if task.State != domain.TaskExecuting {
		t.Fatalf("task state = %s, want %s (transition must not be blocked)", task.State, domain.TaskExecuting)
	}
	// The failure must be surfaced loudly, not swallowed.
	if !strings.Contains(logBuf.String(), "NOT recorded in audit chain") {
		t.Fatalf("audit-write failure must be logged loudly, got log output: %q", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "CREATED -> EXECUTING") {
		t.Fatalf("loud log should name the transition, got: %q", logBuf.String())
	}
}

// TestTaskTransitionAuditNoopWhenUnwired verifies back-compat: a service
// without an audit log (nil) records nothing and transitions normally.
func TestTaskTransitionAuditNoopWhenUnwired(t *testing.T) {
	root := auditTestRoot(t)
	ts := NewTaskService(&Platform{root: root}, nil).WithAuditLog(nil)
	if ts.auditLog != nil {
		t.Fatal("WithAuditLog(nil) must clear the audit log (no-op mode)")
	}

	task, err := ts.Create("noop audit test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ts.transition(task, domain.TaskExecuting); err != nil {
		t.Fatalf("transition without an audit log must still succeed: %v", err)
	}
	if task.State != domain.TaskExecuting {
		t.Fatalf("task state = %s, want %s", task.State, domain.TaskExecuting)
	}
	entries, err := ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	for _, e := range entries {
		if e.Action == "task.transition" {
			t.Fatalf("unwired service must not write task.transition entries, got %+v", e)
		}
	}
}

// failingAuditStore is a storage.Store whose every write fails, simulating an
// unwritable audit directory (disk full / permissions) for the failure-path
// test.
type failingAuditStore struct{}

func (failingAuditStore) Put(_ context.Context, _ string, _ json.RawMessage) error {
	return errors.New("audit store write failed: disk full")
}
func (failingAuditStore) Get(_ context.Context, _ string) (json.RawMessage, error) {
	return nil, errors.New("not found")
}
func (failingAuditStore) Delete(_ context.Context, _ string) error { return nil }
func (failingAuditStore) List(_ context.Context) ([]storage.Entry, error) {
	return nil, nil
}

// TestTaskTransitionAuditedRealPath verifies the production-shaped wiring: a
// full transition path through a TaskService method (not the internal helper)
// lands in the chain. It reuses the observed transition via the platform's
// firewall so the entry shape matches what firewall writers produce.
func TestTaskTransitionAuditedRealPath(t *testing.T) {
	root := auditTestRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("audit-agent")

	task, err := ts.Create("real path audit")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Drive a legal lifecycle through the service-level transition helper.
	for _, next := range []domain.TaskState{domain.TaskExecuting, domain.TaskVerifying, domain.TaskReadyForPR} {
		if err := ts.transition(task, next); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}

	entries, err := ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	var transitions []governance.AuditEntry
	for _, e := range entries {
		if e.Action == "task.transition" {
			transitions = append(transitions, e)
		}
	}
	if len(transitions) != 3 {
		t.Fatalf("expected 3 task.transition entries, got %d: %+v", len(transitions), transitions)
	}
	// The unified chain shares the platform firewall's persisted store: the
	// same entries are visible through the firewall's audit log (replayed
	// store) and the file store the CLI reads.
	if _, err := os.Stat(filepath.Join(root, ".kern", "audit")); err != nil {
		t.Fatalf("audit store dir missing: %v", err)
	}
	if p.Firewall().AuditLog().TotalRecords() < int64(len(transitions)) {
		t.Fatalf("firewall audit log should hold the transition entries, got %d records", p.Firewall().AuditLog().TotalRecords())
	}
}
