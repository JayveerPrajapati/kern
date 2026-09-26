package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// seedPolicyApproval adds a pending approval with the full binding context
// and immediately decides it (approved or rejected), mirroring how the
// workflow engine and CLI record decisions.
func seedPolicyApproval(t *testing.T, store *governance.FileStore, id, requester string, risk domain.RiskLevel, policies []string, artifact string, approved bool) {
	t.Helper()
	if err := store.AddPending(domain.Approval{
		ID: id, TaskID: "task-" + id, Requester: requester, Status: "pending",
		RiskLevel: risk, PolicyIDs: policies, ArtifactID: artifact, RequestedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AddPending(%s): %v", id, err)
	}
	if _, err := store.Decide(id, "human", approved, ""); err != nil {
		t.Fatalf("Decide(%s): %v", id, err)
	}
}

// TestRecordPolicySignalsWritesRecommendation proves the full learning pass:
// three decided approvals of one action signature write exactly one
// RECOMMENDATION typed-claim memory through the learning path (deterministic
// statement, signature scope, contributing approvals as provenance), and a
// second run upserts the same scope instead of duplicating (idempotent).
func TestRecordPolicySignalsWritesRecommendation(t *testing.T) {
	root := t.TempDir()
	store := governance.NewFileStore(root)
	for i := 0; i < 3; i++ {
		seedPolicyApproval(t, store, fmt.Sprintf("appr-%d", i), "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", true)
	}
	mem := memory.NewMemoryStore(root)

	n, err := RecordPolicySignals(store, mem, DefaultPolicySignalThreshold)
	if err != nil {
		t.Fatalf("RecordPolicySignals: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", m.ClaimType)
	}
	if !strings.HasPrefix(m.Scope, "approval:agent-a:HIGH:") {
		t.Errorf("Scope = %q, want the approval signature", m.Scope)
	}
	if !strings.Contains(m.Content, "pre-approve agent-a HIGH action (p1,p2) — approved 3 times, never rejected") {
		t.Errorf("Content = %q, want the pre-approval statement", m.Content)
	}
	if !strings.Contains(m.Provenance, "approval appr-0") || !strings.Contains(m.Provenance, "approval appr-2") {
		t.Errorf("Provenance = %q, want the contributing approval IDs", m.Provenance)
	}

	// Idempotent re-run: the scope upsert keeps exactly one memory.
	n2, err := RecordPolicySignals(store, mem, DefaultPolicySignalThreshold)
	if err != nil {
		t.Fatalf("RecordPolicySignals (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1", n2)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("idempotency broken: memories = %d, want 1", len(mems))
	}
}

// TestRecordPolicySignalsNilGuards proves the nil-guard contract: a nil store
// or nil memory store is a no-op (0, nil) that never panics, so unwired paths
// keep their zero behavior change.
func TestRecordPolicySignalsNilGuards(t *testing.T) {
	mem := memory.NewMemoryStore(t.TempDir())
	if n, err := RecordPolicySignals(nil, mem, DefaultPolicySignalThreshold); n != 0 || err != nil {
		t.Errorf("nil store = (%d, %v), want (0, nil)", n, err)
	}
	store := governance.NewFileStore(t.TempDir())
	if n, err := RecordPolicySignals(store, nil, DefaultPolicySignalThreshold); n != 0 || err != nil {
		t.Errorf("nil mem = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := RecordPolicySignals(nil, nil, DefaultPolicySignalThreshold); n != 0 || err != nil {
		t.Errorf("nil both = (%d, %v), want (0, nil)", n, err)
	}
}
