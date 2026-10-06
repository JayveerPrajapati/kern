package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/flock"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// readAuditEntries reads every persisted entry from the audit store under
// <root>/.kern/audit — the same store `kern audit` and the service layer read.
func readAuditEntries(t *testing.T, root string) []AuditEntry {
	t.Helper()
	store := storage.NewLog(filepath.Join(root, ".kern", "audit"))
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("audit store List: %v", err)
	}
	var out []AuditEntry
	for _, e := range entries {
		var entry AuditEntry
		if err := storage.UnmarshalValue(e.Value, &entry); err != nil {
			t.Fatalf("unmarshal audit entry: %v", err)
		}
		out = append(out, entry)
	}
	return out
}

func TestFileStoreDecideRecordsApprovalDecision(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)

	a := domain.Approval{
		ID:          "appr-audit-1",
		TaskID:      "t-42",
		Requester:   "planner",
		Status:      "pending",
		Reason:      "high-risk change",
		RequestedAt: time.Now(),
	}
	if err := store.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	decided, err := store.Decide(a.ID, "human-1", true, "looks good")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != "approved" {
		t.Fatalf("decided.Status = %q, want approved", decided.Status)
	}

	// The decision must be visible from the persisted chain, not just memory.
	entries := readAuditEntries(t, root)
	var found *AuditEntry
	for i := range entries {
		if entries[i].Action == "approve" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no approve audit entry after Decide; entries: %+v", entries)
	}
	if found.TaskID != "t-42" {
		t.Errorf("entry TaskID = %q, want t-42", found.TaskID)
	}
	if found.AgentID != "human-1" {
		t.Errorf("entry AgentID = %q, want human-1", found.AgentID)
	}
	if found.Resource != "approval:appr-audit-1" {
		t.Errorf("entry Resource = %q, want approval:appr-audit-1", found.Resource)
	}
	if !found.Approved {
		t.Error("entry Approved = false, want true")
	}
	if found.Result != "approved" {
		t.Errorf("entry Result = %q, want approved", found.Result)
	}
	if found.Hash == "" {
		t.Error("decision entry must be hash-chained into the tamper-evident chain")
	}
	if found.Timestamp.IsZero() {
		t.Error("decision entry must carry a timestamp")
	}

	// The chain must verify end-to-end (tamper-evident).
	replayed := NewAuditLog().WithStore(storage.NewLog(filepath.Join(root, ".kern", "audit")))
	if _, err := replayed.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed.VerifyChain() {
		t.Fatal("VerifyChain() = false: decision entry broke the tamper chain")
	}
}

func TestFileStoreRejectRecordsApprovalDecision(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)

	a := domain.Approval{
		ID:          "appr-audit-2",
		TaskID:      "t-7",
		Requester:   "planner",
		Status:      "pending",
		RequestedAt: time.Now(),
	}
	if err := store.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}

	if _, err := store.Decide(a.ID, "human-2", false, "not in scope"); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}

	entries := readAuditEntries(t, root)
	var found *AuditEntry
	for i := range entries {
		if entries[i].Action == "reject" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no reject audit entry after Decide; entries: %+v", entries)
	}
	if found.Result != "denied" {
		t.Errorf("entry Result = %q, want denied", found.Result)
	}
	if found.Approved {
		t.Error("entry Approved = true, want false")
	}
	if found.TaskID != "t-7" || found.AgentID != "human-2" {
		t.Errorf("entry TaskID/AgentID = %q/%q, want t-7/human-2", found.TaskID, found.AgentID)
	}
}

// TestFileStoreDecideDoesNotHangWhenAuditLockHeld covers the real-world hang
// this batch fixed: the approve/reject decision must persist and return
// promptly even when the audit chain is contended. The audit append
// (recordAudit → AppendExternal) is best-effort AND bounded — a held lock
// makes it fail loudly within the retry budget, never block the decision.
func TestFileStoreDecideDoesNotHangWhenAuditLockHeld(t *testing.T) {
	root := t.TempDir()
	auditDir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	holder, err := flock.TryLock(filepath.Join(auditDir, ".lock"))
	if err != nil {
		t.Fatalf("hold audit lock: %v", err)
	}
	defer func() { _ = flock.Release(holder) }()

	store := NewFileStore(root)
	if err := store.AddPending(domain.Approval{ID: "apr-lock", Status: "pending"}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	start := time.Now()
	decided, err := store.Decide("apr-lock", "human", true, "test")
	if err != nil {
		t.Fatalf("Decide must not fail when only the audit chain is contended: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Decide blocked for %s — must return promptly when the audit lock is held", elapsed)
	}
	if decided.Status != "approved" {
		t.Errorf("decided.Status = %q, want approved", decided.Status)
	}
}
