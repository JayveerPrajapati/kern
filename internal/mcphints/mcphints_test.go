package mcphints

import (
	"strings"
	"testing"
)

// TestSelectPerToolOutcome: each (tool, outcome) table entry selects its
// line, and unlisted combinations select nothing.
func TestSelectPerToolOutcome(t *testing.T) {
	h := New()
	cases := []struct {
		tool    string
		outcome Outcome
		want    string
	}{
		{"kern_explore", OutcomeSuccess, "next: kern_why names who depends on this; kern_impact before editing"},
		{"kern_plan", OutcomeSuccess, "next: recall prior lessons (kern_memory recall) before implementing"},
		{"kern_verify", OutcomeFailure, "next: kern_heal can auto-repair failing files in a snapshot"},
		{"kern_search", OutcomeNoMatch, "try kern_meta (NL router) or kern_buddy for orientation"},
		// Unlisted combinations select nothing.
		{"kern_search", OutcomeSuccess, ""},
		{"kern_explore", OutcomeNoMatch, ""},
		{"kern_memory", OutcomeSuccess, ""},
	}
	for _, c := range cases {
		if got := h.Select("s", c.tool, c.outcome); got != c.want {
			t.Errorf("Select(%s, %v) = %q, want %q", c.tool, c.outcome, got, c.want)
		}
	}
}

// TestSelectOncePerSession: a given hint fires at most once per session —
// the second identical selection is empty — while a different session still
// gets it.
func TestSelectOncePerSession(t *testing.T) {
	h := New()
	if got := h.Select("alice", "kern_explore", OutcomeSuccess); got == "" {
		t.Fatal("first selection must fire")
	}
	if got := h.Select("alice", "kern_explore", OutcomeSuccess); got != "" {
		t.Fatalf("a fired hint must not re-fire in the same session, got %q", got)
	}
	// A different session key is untouched.
	if got := h.Select("bob", "kern_explore", OutcomeSuccess); got == "" {
		t.Fatal("a fresh session must still get the hint")
	}
	// A different (tool, outcome) hint is independent.
	if got := h.Select("alice", "kern_plan", OutcomeSuccess); got == "" {
		t.Fatal("a different hint must still fire in the same session")
	}
}

// TestEnabledEnvOff: KERN_HINTS=0 disables selection entirely — nothing
// fires and nothing is recorded.
func TestEnabledEnvOff(t *testing.T) {
	t.Setenv("KERN_HINTS", "0")
	h := New()
	if h.Enabled() {
		t.Fatal("KERN_HINTS=0 must disable hints")
	}
	if got := h.Select("s", "kern_explore", OutcomeSuccess); got != "" {
		t.Fatalf("disabled hints must select nothing, got %q", got)
	}
	if got := h.Gap("s", GapThreshold, map[string]bool{}); got != "" {
		t.Fatalf("disabled hints must not gap-nudge, got %q", got)
	}
	// Re-enable: the disabled-era selections were never recorded.
	t.Setenv("KERN_HINTS", "")
	if !h.Enabled() {
		t.Fatal("unset KERN_HINTS must enable hints (default ON)")
	}
	if got := h.Select("s", "kern_explore", OutcomeSuccess); got == "" {
		t.Fatal("a hint not fired while disabled must still fire after re-enabling")
	}
}

// TestGapThresholdAndOncePerTool: the gap hint fires only at/above the
// read-call threshold, names the first ranked underused tool the session has
// not used, and fires at most once per session per tool.
func TestGapThresholdAndOncePerTool(t *testing.T) {
	h := New()
	// Below the threshold: no nudge.
	if got := h.Gap("s", GapThreshold-1, map[string]bool{}); got != "" {
		t.Fatalf("below the threshold must not nudge, got %q", got)
	}
	// At the threshold with nothing used: the most underused tool wins.
	got := h.Gap("s", GapThreshold, map[string]bool{})
	if !strings.Contains(got, "kern_semcache") {
		t.Fatalf("gap nudge must name the top-ranked underused tool, got %q", got)
	}
	// Once per session per tool: the same tool never re-nudges.
	if got := h.Gap("s", GapThreshold+5, map[string]bool{}); got != "" {
		t.Fatalf("an already-nudged tool must not re-nudge, got %q", got)
	}
	// A fresh session gets its own nudge.
	if got := h.Gap("other", GapThreshold, map[string]bool{}); got == "" || !strings.Contains(got, "kern_semcache") {
		t.Fatalf("a fresh session must get the gap nudge, got %q", got)
	}
}

// TestGapSuppressedWhenToolUsed: a session that already used a top-ranked
// underused tool is nudged toward the NEXT unused one, and a session that
// used every ranked tool gets no nudge at all.
func TestGapSuppressedWhenToolUsed(t *testing.T) {
	h := New()
	// kern_semcache used → the nudge names kern_why instead.
	got := h.Gap("s", GapThreshold, map[string]bool{"kern_semcache": true})
	if !strings.Contains(got, "kern_why") {
		t.Fatalf("used semcache must advance the nudge to the next underused tool, got %q", got)
	}
	// Every ranked tool used → no nudge.
	all := map[string]bool{"kern_semcache": true, "kern_why": true, "kern_plan": true, "kern_memory": true, "kern_impact": true}
	if got := h.Gap("s2", GapThreshold, all); got != "" {
		t.Fatalf("no unused underused tool must suppress the nudge, got %q", got)
	}
}

// TestTrackFeedsGap: Track records usage and the read counter; a used tool
// never appears in the gap nudge, and the read count gates the threshold.
func TestTrackFeedsGap(t *testing.T) {
	h := New()
	h.Track("s", "kern_explore", true)
	h.Track("s", "kern_memory", false) // used but not a read call
	if got := h.Reads("s"); got != 1 {
		t.Fatalf("Reads after one read call = %d, want 1", got)
	}
	if !h.Used("s")["kern_memory"] {
		t.Fatal("Track must record the used tool set")
	}
	// readCalls below the threshold despite the used set: no nudge.
	if got := h.Gap("s", h.Reads("s"), h.Used("s")); got != "" {
		t.Fatalf("below-threshold session must not nudge, got %q", got)
	}
	// 10 reads with kern_memory used → nudge names the next unused tool.
	for i := 0; i < 9; i++ {
		h.Track("s", "kern_search", true)
	}
	got := h.Gap("s", h.Reads("s"), h.Used("s"))
	if !strings.Contains(got, "kern_semcache") {
		t.Fatalf("gap nudge must name the top-ranked unused tool, got %q", got)
	}
}
