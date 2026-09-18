package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/gates"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestRunApprovalDecisionWritesGovernanceChain verifies the R-8 follow-up:
// the bpcli reject/approve decision path (RunApprovalDecision, used by
// `kern reject <id>` and `blueprint approve/reject <id>`) appends a
// governance-style entry to the tamper-evident .kern/audit chain — same
// shape as governance.FileStore.recordAudit (Action approve/reject, Resource
// "approval:<id>", Policy "approval", Result approved/denied) — so `kern
// audit` no longer has to re-derive the decision at render time. The chain
// must stay intact. The E-3 pipeline commit/BLOCK link may add a second
// entry when a kern binary is resolvable, so integrity is asserted over all
// entries rather than a fixed count.
func TestRunApprovalDecisionWritesGovernanceChain(t *testing.T) {
	for _, tc := range []struct {
		decision string
		wantAct  string
		wantRes  string
		wantAppr bool
	}{
		{"reject", "reject", "denied", false},
		{"approve", "approve", "approved", true},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			root := t.TempDir()
			id := "apr-cli-" + tc.decision
			if err := gates.NewStore(root).Create(gates.Request{
				ID:        id,
				Intent:    "deploy the release",
				Requester: "alice",
				CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}

			if rc := RunApprovalDecision(tc.decision, []string{
				"--repo", root, "--approver", "qa-bot", "--reason", "reviewed on call", id,
			}); rc != 0 {
				t.Fatalf("RunApprovalDecision(%s) rc=%d, want 0", tc.decision, rc)
			}

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
			if found.Action != tc.wantAct || found.Result != tc.wantRes || found.Approved != tc.wantAppr {
				t.Errorf("chain entry = action=%s result=%s approved=%t, want %s/%s/%t",
					found.Action, found.Result, found.Approved, tc.wantAct, tc.wantRes, tc.wantAppr)
			}
			if found.AgentID != "qa-bot" || found.Policy != "approval" {
				t.Errorf("chain entry agent=%s policy=%s, want qa-bot/approval", found.AgentID, found.Policy)
			}
			if found.Timestamp.IsZero() {
				t.Error("chain entry has zero timestamp")
			}
			// Integrity: no broken link; every entry (possibly including the
			// E-3 pipeline commit/BLOCK link) must verify.
			if brk, verified := l.VerifyChainReport(); brk >= 0 || verified < 1 {
				t.Errorf("chain integrity: verified=%d firstBroken=%d, want verified>=1 and no break", verified, brk)
			}
		})
	}
}
