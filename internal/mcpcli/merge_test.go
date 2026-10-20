package mcpcli

import "testing"

// TestRunSemanticDiffBadFlagExits2 pins the uniform flag-error handling: a
// bad flag exits 2 with a "flags:" usage error, never a silent default.
func TestRunSemanticDiffBadFlagExits2(t *testing.T) {
	if code := RunSemanticDiff([]string{"--bogus"}); code != 2 {
		t.Fatalf("RunSemanticDiff(--bogus) = %d, want 2 (usage error)", code)
	}
}

// TestRunSemanticMergeBadFlagExits2 pins the same contract for the merge
// command.
func TestRunSemanticMergeBadFlagExits2(t *testing.T) {
	if code := RunSemanticMerge([]string{"--bogus"}); code != 2 {
		t.Fatalf("RunSemanticMerge(--bogus) = %d, want 2 (usage error)", code)
	}
}
