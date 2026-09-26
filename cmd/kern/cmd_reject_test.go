package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/gates"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestRejectDispatchRoutesToRunReject locks the dispatch re-bind in
// cmd_reject.go: `kern reject` must route to runReject (the two-store
// resolver shared with runApprove), not the bpcli single-store runner.
// An unknown id through runReject fails loudly with exitError{1}; bpcli would
// return 3 as a plain exit code (no sentinel panic).
func TestRejectDispatchRoutesToRunReject(t *testing.T) {
	e, ok := commandTable["reject"]
	if !ok {
		t.Fatal("commandTable has no reject entry")
	}
	if e.help == "" {
		t.Error("reject entry has empty help")
	}
	expectExit(t, 1, func() {
		dispatchCommand("reject", []string{"--root", t.TempDir(), "apr-nope"})
	})
}

// TestRejectGovernanceApproval locks the governance-store half of the QA
// finding: `kern reject <id>` must resolve an approval from
// .kern/approvals.json — the store `kern approve` lists and resolves FIRST —
// instead of failing "not found" (the previous bpcli-only reject looked only
// in the blueprint store).
func TestRejectGovernanceApproval(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	out := captureStdout(t, func() {
		runReject([]string{"--root", root, id, "--reason", "not now", "--approver", "senior"})
	})
	for _, want := range []string{"rejected: " + id, "(by senior)"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject output missing %q:\n%s", want, out)
		}
	}
	// The store must no longer list the approval as pending.
	out = captureStdout(t, func() { runApprove([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no pending approvals") {
		t.Errorf("approval should be decided; listing:\n%s", out)
	}
}

// TestRejectBlueprintApproval locks the blueprint half: `kern reject <apr-*>`
// must still resolve ids created by `kern request-approval`
// (.blueprint/approvals/requests.jsonl) through the shared core, and the
// decision must land in the tamper-evident audit chain exactly like
// runApprove's blueprint path (D8).
func TestRejectBlueprintApproval(t *testing.T) {
	root := newRoot(t)
	id := "apr-reject-test"
	if err := gates.NewStore(root).Create(gates.Request{
		ID:        id,
		Intent:    "deploy the release",
		Requester: "alice",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("blueprint Create: %v", err)
	}
	out := captureStdout(t, func() {
		runReject([]string{"--root", root, id, "--reason", "qa test", "--approver", "senior"})
	})
	for _, want := range []string{"rejected: " + id, "(by senior)"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject output missing %q:\n%s", want, out)
		}
	}
	cur, err := gates.NewStore(root).Get(id)
	if err != nil {
		t.Fatalf("Get after reject: %v", err)
	}
	if cur.Status != gates.StatusRejected {
		t.Errorf("blueprint status = %s, want %s", cur.Status, gates.StatusRejected)
	}
	assertAuditEntry(t, root, id, "reject", "denied", false, "senior")
}

// TestRejectUnknownID locks the not-found contract: an id that exists in
// neither store exits 1 with the shared "approval <id> not found" error,
// never a silent success.
func TestRejectUnknownID(t *testing.T) {
	root := newRoot(t)
	expectExit(t, 1, func() { runReject([]string{"--root", root, "apr-nonexistent"}) })
}

// TestRejectGovernanceAlreadyDecidedExits3 locks the shared decided-state
// guard: rejecting an already-decided governance approval is a policy outcome
// (rc=3), the same convention `kern approve` uses.
func TestRejectGovernanceAlreadyDecidedExits3(t *testing.T) {
	root := newRoot(t)
	store := governance.NewFileStore(root)
	if err := store.AddPending(domain.Approval{ID: "apr-rej-gov", Status: "pending", TaskID: ""}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	if _, err := store.Decide("apr-rej-gov", "cli-user", true, ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	expectExit(t, 3, func() { runReject([]string{"--root", root, "apr-rej-gov"}) })
}

// TestRejectBlueprintAlreadyDecidedExits3 locks the blueprint decided-state
// guard through the shared core (rc=3, matching runApprove's mapping of the
// blueprint "already decided" error).
func TestRejectBlueprintAlreadyDecidedExits3(t *testing.T) {
	root := newRoot(t)
	id := "apr-rej-bp"
	if err := gates.NewStore(root).Create(gates.Request{
		ID:        id,
		Intent:    "deploy the release",
		Requester: "alice",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("blueprint Create: %v", err)
	}
	if err := gates.NewStore(root).Reject(id, "qa-bot", "already decided"); err != nil {
		t.Fatalf("blueprint Reject: %v", err)
	}
	expectExit(t, 3, func() { runReject([]string{"--root", root, id}) })
}

// assertAuditEntry verifies the tamper-evident .kern/audit chain carries a
// governance-shaped decision entry for the given approval (same shape
// FileStore.recordAudit uses), mirroring the assertion in
// TestBlueprintDecisionWritesAuditChain.
func assertAuditEntry(t *testing.T, root, id, wantAct, wantRes string, wantAppr bool, wantAgent string) {
	t.Helper()
	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	var found *governance.AuditEntry
	for i := range l.All() {
		e := &l.All()[i]
		if e.Resource == "approval:"+id {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("audit chain has no entry for %s; entries: %+v", id, l.All())
	}
	if found.Action != wantAct || found.Result != wantRes || found.Approved != wantAppr {
		t.Errorf("chain entry = action=%s result=%s approved=%t, want %s/%s/%t",
			found.Action, found.Result, found.Approved, wantAct, wantRes, wantAppr)
	}
	if found.AgentID != wantAgent || found.Policy != "approval" {
		t.Errorf("chain entry agent=%s policy=%s, want %s/approval", found.AgentID, found.Policy, wantAgent)
	}
	if found.Timestamp.IsZero() {
		t.Error("chain entry has zero timestamp")
	}
	if brk, verified := l.VerifyChainReport(); verified != 1 || brk >= 0 {
		t.Errorf("chain integrity: verified=%d firstBroken=%d, want 1/-1", verified, brk)
	}
}
