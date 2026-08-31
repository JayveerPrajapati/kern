package app

import (
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestAuditEntriesReadsPersistedStore verifies AuditEntries reads the persisted
// trail (not just the in-memory AuditLog): a fresh TaskService process must see
// entries written by a prior process, which the MCP kern_audit tool depends on.
func TestAuditEntriesReadsPersistedStore(t *testing.T) {
	root := t.TempDir()
	ts := NewTaskService(&Platform{root: root}, nil)

	// Simulate a prior process: write entries to the persisted store directly.
	log := governance.NewAuditLog().WithStore(storage.NewLocal(filepath.Join(root, ".kern", "audit")))
	log.Record(governance.AuditEntry{AgentID: "agent-1", Action: "approve", Resource: "deploy", Approved: true, Result: "allowed", TaskID: "t-1"})
	log.Record(governance.AuditEntry{AgentID: "agent-2", Action: "deny", Resource: "runtime", Approved: false, Result: "blocked", TaskID: "t-2"})
	log.Record(governance.AuditEntry{AgentID: "agent-1", Action: "run", Resource: "loop", Approved: true, Result: "allowed", TaskID: "t-1"})

	entries, err := ts.AuditEntries()
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("AuditEntries() = %d entries, want 3", len(entries))
	}

	t1, err := ts.AuditEntriesForTask("t-1")
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	if len(t1) != 2 {
		t.Fatalf("AuditEntriesForTask(t-1) = %d entries, want 2", len(t1))
	}
	for _, e := range t1 {
		if e.TaskID != "t-1" {
			t.Errorf("AuditEntriesForTask(t-1) returned entry with TaskID %q", e.TaskID)
		}
	}

	t2, err := ts.AuditEntriesForTask("t-2")
	if err != nil {
		t.Fatalf("AuditEntriesForTask(t-2): %v", err)
	}
	if len(t2) != 1 {
		t.Fatalf("AuditEntriesForTask(t-2) = %d entries, want 1", len(t2))
	}

	missing, err := ts.AuditEntriesForTask("t-999")
	if err != nil {
		t.Fatalf("AuditEntriesForTask(t-999): %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("AuditEntriesForTask(t-999) = %d entries, want 0", len(missing))
	}
}

// TestPendingApprovalsAndResolveRoundTrip verifies PendingApprovals /
// ResolveApproval round-trip through the shared persistent store: a decision
// made via the service is visible to a fresh store (and vice-versa), which is
// what the CLI `kern approve` and the MCP kern_approve tool rely on.
func TestPendingApprovalsAndResolveRoundTrip(t *testing.T) {
	root := t.TempDir()
	ts := NewTaskService(&Platform{root: root}, nil)

	// Seed a pending approval the way a workflow/deploy gate would.
	store := governance.NewFileStore(root)
	if err := store.AddPending(domain.Approval{ID: "a-1", TaskID: "t-1", Requester: "engine", Reason: "deploy gate"}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	pending, err := ts.PendingApprovals()
	if err != nil {
		t.Fatalf("PendingApprovals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingApprovals() = %d approvals, want 1", len(pending))
	}
	if pending[0].ID != "a-1" {
		t.Fatalf("PendingApprovals()[0].ID = %q, want a-1", pending[0].ID)
	}

	decided, err := ts.ResolveApproval("a-1", "cli-user", true, "looks good")
	if err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if decided.Status != "approved" {
		t.Fatalf("ResolveApproval status = %q, want approved", decided.Status)
	}
	if decided.Approver != "cli-user" {
		t.Fatalf("ResolveApproval approver = %q, want cli-user", decided.Approver)
	}

	// The approval must no longer be pending.
	pending, err = ts.PendingApprovals()
	if err != nil {
		t.Fatalf("PendingApprovals after resolve: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("PendingApprovals() after resolve = %d approvals, want 0", len(pending))
	}

	// The decision must be persisted: a fresh store sees it.
	got, err := store.Get("a-1")
	if err != nil {
		t.Fatalf("store.Get(a-1): %v", err)
	}
	if got.Status != "approved" || got.Approver != "cli-user" {
		t.Errorf("persisted approval = %+v, want approved by cli-user", got)
	}
}
