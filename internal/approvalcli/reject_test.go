package approvalcli

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestRejectGovernanceApproval locks the governance-store half of the QA
// finding: `kern reject <id>` must resolve an approval from
// .kern/approvals.json — the store `kern approve` lists and resolves FIRST —
// instead of failing "not found" (the previous bpcli-only reject looked only
// in the blueprint store).
func TestRejectGovernanceApproval(t *testing.T) {
	root := newRoot(t)
	id := approvalFixture(t, root, "", "alice", "deploy to prod")
	out := captureStdout(t, func() {
		RunReject([]string{"--root", root, id, "--reason", "not now", "--approver", "senior"})
	})
	for _, want := range []string{"rejected: " + id, "(by senior)"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject output missing %q:\n%s", want, out)
		}
	}
	// The store must no longer list the approval as pending.
	out = captureStdout(t, func() { RunApprove([]string{"list", "--root", root}) })
	if !strings.Contains(out, "no pending approvals") {
		t.Errorf("approval should be decided; listing:\n%s", out)
	}
}

// TestRejectBlueprintApproval locks the blueprint half: `kern reject <apr-*>`
// must still resolve ids created by `kern request-approval`
// (.blueprint/approvals/requests.jsonl) through the shared core, and the
// decision must land in the tamper-evident audit chain exactly like
// runApprove's blueprint path (D8).
func TestRejectUnknownID(t *testing.T) {
	root := newRoot(t)
	expectCode(t, 1, func() int { return RunReject([]string{"--root", root, "apr-nonexistent"}) })
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
	expectCode(t, 3, func() int { return RunReject([]string{"--root", root, "apr-rej-gov"}) })
}

// TestRejectBlueprintAlreadyDecidedExits3 locks the blueprint decided-state
// guard through the shared core (rc=3, matching runApprove's mapping of the
// blueprint "already decided" error).
