package verification

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// TestInheritNoteNotMarkedSkipped pins the other half of the nested-sandbox
// inherit design: the engine's isolation-skip matcher keys on the sandbox's
// REFUSAL text ("refusing to run unisolated"). A run that INHERITED the
// outer sandbox (its output carries the inherit note's "inheriting outer
// network isolation" text) actually EXECUTED inside the outer profile — so
// a failing set with that note must stay a FAIL, never a skip, and a
// passing set stays PASS.
func TestInheritNoteNotMarkedSkipped(t *testing.T) {
	res := verdict.VerificationResult{
		Build: &verdict.BuildResult{OK: true},
		UnitTests: &verdict.TestResult{
			OK:     false,
			Output: "note: already inside an active kern sandbox (KERN_SANDBOX_ACTIVE=1); inheriting outer network isolation — not re-isolating\n",
		},
	}
	markIsolationSkipped(&res)
	if res.UnitTests.Status == verdict.StatusSkipped {
		t.Fatalf("the inherit note must never be mistaken for a refusal skip")
	}
	res.Verdict = verdict.DeriveVerdict(&res)
	if res.Verdict != verdict.VerdictFail {
		t.Fatalf("a failed set that ran under an inherited sandbox stays FAIL, got %q", res.Verdict)
	}

	// A passing set carrying the inherit note must stay PASS untouched.
	passing := verdict.VerificationResult{
		Build:     &verdict.BuildResult{OK: true},
		UnitTests: &verdict.TestResult{OK: true, Output: "note: already inside an active kern sandbox (KERN_SANDBOX_ACTIVE=1); inheriting outer network isolation — not re-isolating\n"},
	}
	markIsolationSkipped(&passing)
	if passing.UnitTests.Status == verdict.StatusSkipped {
		t.Fatalf("a passing inherited run must not be marked skipped")
	}
	passing.Verdict = verdict.DeriveVerdict(&passing)
	if passing.Verdict != verdict.VerdictPass {
		t.Fatalf("a passing inherited run stays PASS, got %q", passing.Verdict)
	}
}
