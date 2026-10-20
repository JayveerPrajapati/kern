package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"github.com/JayveerPrajapati/kern/internal/optimize"
)

// TestPrintTokenSavingsOnlyOnRealSavings pins F11: the "kern: X -> Y tokens
// (saved N, P%…)" accounting line prints only on a genuine reduction; equal
// or inflated results are silent (no "saved 0, 0.0%" noise).
func TestPrintTokenSavingsOnlyOnRealSavings(t *testing.T) {
	var b strings.Builder
	printTokenSavings(&b, 1000, 400, ", budget 4000", " [window -1/+1]")
	want := "kern: 1000 -> 400 tokens (saved 600, 60.0%, budget 4000) [window -1/+1]\n"
	if b.String() != want {
		t.Errorf("reduced: got %q, want %q", b.String(), want)
	}
	for _, c := range []struct {
		name          string
		before, after int
	}{
		{"equal", 500, 500},
		{"inflated", 500, 600},
	} {
		var silent strings.Builder
		printTokenSavings(&silent, c.before, c.after, ", budget 4000", " [window -1/+1]")
		if silent.String() != "" {
			t.Errorf("%s: expected silence, got %q", c.name, silent.String())
		}
	}
}

// TestWireRecorder pins wireRecorder's contract: it must not panic and must be
// safe to call repeatedly. XDG_CACHE_HOME is isolated so the stats recorder
// (rooted at <cache>/kern/stats) never touches the real user cache.
func TestWireRecorder(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	wireRecorder()
	wireRecorder() // repeated calls must be safe

	if optimize.Recorder == nil {
		t.Fatal("wireRecorder: optimize.Recorder not wired")
	}
}

// TestVerifyTypeVocabularyAcceptsReuse pins CLI-surface parity with the
// engine's "reuse" advisory check (VerifyTypesKnown / KnownVerifyType in
// internal/mcpcatalog accept it): the shared type gate behind the CLI's
// --types validation and the isVerifyTypes disambiguation gate must accept
// "reuse", so `kern verify reuse` runs the check instead of being misread
// as the claims-verification form or rejected as unknown.
func TestVerifyTypeVocabularyAcceptsReuse(t *testing.T) {
	if !catalog.KnownVerifyType("reuse") {
		t.Error(`KnownVerifyType("reuse") = false, want true (engine accepts the reuse advisory type)`)
	}
	if !isVerifyTypes("reuse") {
		t.Error(`isVerifyTypes("reuse") = false, want true (kern verify reuse would be misread as claims verification)`)
	}
	if isVerifyTypes("reuse,bogus") {
		t.Error(`isVerifyTypes("reuse,bogus") = true, want false (unknown members must still be rejected)`)
	}
}
