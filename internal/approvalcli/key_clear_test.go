package approvalcli

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestIntegrityNoteDistinguishesRotation is the regression pin for the false
// tamper accusation: a record whose MAC predates the current key must never
// read as hand-edited content.
func TestIntegrityNoteDistinguishesRotation(t *testing.T) {
	rot := integrityNote(governance.IntegrityReport{Verdict: governance.IntegrityKeyRotated, Reason: "MAC key was created later"})
	if !strings.Contains(rot, "key-rotated") || !strings.Contains(rot, "clear --stale") {
		t.Errorf("key-rotated note must name the cause and the fix: %q", rot)
	}
	if strings.Contains(rot, "modified outside") || strings.Contains(rot, "DO NOT APPROVE") {
		t.Errorf("key-rotated note must not accuse the operator of tampering: %q", rot)
	}
	tam := integrityNote(governance.IntegrityReport{Verdict: governance.IntegrityTampered, Reason: "edited"})
	if !strings.Contains(tam, "tampered") || !strings.Contains(tam, "DO NOT APPROVE") {
		t.Errorf("tampered note must keep the hard warning: %q", tam)
	}
	sig := integrityNote(governance.IntegrityReport{Verdict: governance.IntegritySignatureUnverifiable, Reason: "old key"})
	if !strings.Contains(sig, "signature") || strings.Contains(sig, "DO NOT APPROVE") {
		t.Errorf("signature note must name the verdict without the tamper wording: %q", sig)
	}
}

// TestApprovalClearStaleIsWired pins the dispatch: `clear --stale` reaches the
// key-family remediation path (an unstamped store has no stale records, so the
// call is a harmless no-op here).
func TestApprovalClearStaleIsWired(t *testing.T) {
	root := newRoot(t)
	seedPendingApproval(t, root, "plain-1")
	var code int
	out := captureStdout(t, func() {
		code = RunApproval([]string{"clear", "--stale", "--yes", "--root", root})
	})
	if code != 0 || !strings.Contains(out, "no stale") {
		t.Fatalf("exit = %d, want 0 with a no-stale notice\n%s", code, out)
	}
	expectCode(t, 2, func() int {
		return RunApproval([]string{"clear", "--stale", "some-id", "--root", root})
	})
}
