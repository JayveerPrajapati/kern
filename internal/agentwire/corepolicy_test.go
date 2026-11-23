package agentwire

import (
	"strings"
	"testing"
)

// TestCoreRepoPolicyCompact pins the T-05 compact-policy contract
// (2026-11-19): the DEFAULT repo policy (the thin/core AGENTS.md) stays
// <=2KB and carries the three load-bearing rules — the direct tool route,
// the verify rule, and the per-call fallback rule — while the condensed
// GLOBAL block stays <=6KB. Breaking any bound regresses the
// instructions-cost gap to densic that motivated T-05.
func TestCoreRepoPolicyCompact(t *testing.T) {
	core := ThinAGENTSmd("all")
	if n := len(core); n > 2048 {
		t.Errorf("default repo policy is %d bytes, want <= 2048:\n%s", n, core)
	}
	for _, want := range []string{
		"kern_search",      // direct tool route
		"kern_explore",     // read routing
		"kern_verify",      // verify rule
		"Before ANY edit",  // blast radius before edit
		"After ANY change", // verify after change
		"Fallback rule",    // per-call fallback
		"kern_buddy",       // onboarding digest
		"kern_meta",        // the unsure router
	} {
		if !strings.Contains(core, want) {
			t.Errorf("default repo policy missing %q:\n%s", want, core)
		}
	}

	global, err := CondenseGlobalRules()
	if err != nil {
		t.Fatalf("condense global rules: %v", err)
	}
	if n := len(global); n > 6144 {
		t.Errorf("condensed global policy is %d bytes, want <= 6144", n)
	}
	// The global block is the full rules minus the global-omit sections —
	// it must still carry the direct-route table (the repo core is thin
	// now, so the global slot is the delivery for the extended policy).
	if !strings.Contains(string(global), "kern_search") {
		t.Errorf("global block lost the direct tool route")
	}
}
