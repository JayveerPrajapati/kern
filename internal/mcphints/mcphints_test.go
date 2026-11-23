package mcphints

import (
	"strings"
	"testing"
)

// TestSelectPerToolOutcome: each (tool, outcome) table entry selects its
// line on a surface where every advertised tool is callable (nil probe),
// and unlisted combinations select nothing.
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
		if got := h.Select("s", c.tool, c.outcome, nil); got != c.want {
			t.Errorf("Select(%s, %v) = %q, want %q", c.tool, c.outcome, got, c.want)
		}
	}
}

// defaultSurface mirrors the eleven-tool default MCP surface
// (internal/mcp/toolsurface.Default, pinned there by its acceptance test).
// Repeated here because this package is deliberately dependency-free
// (ledger: empty allowed-deps); the set is asserted against the real
// surface in internal/mcp's own tests.
var defaultSurface = map[string]bool{
	"kern_buddy": true, "kern_search": true, "kern_explore": true,
	"kern_context": true, "kern_probe": true, "kern_research": true,
	"kern_impact": true, "kern_graph": true, "kern_review": true,
	"kern_verify": true, "kern_meta": true,
}

// onSurface adapts a membership map to the availability probe.
func onSurface(m map[string]bool) func(string) bool {
	return func(name string) bool { return m[name] }
}

// TestSelectSurfaceAware (F-B4 regression, dogfood 2026-11-22 lane B): on
// the default surface the explore hint must NOT advertise kern_why (a
// KERN_MCP_FULL-only tool whose call yields JSON-RPC -32602 unknown tool) —
// it falls back to the kern_impact-only alternative; hints whose every
// alternative names off-surface tools are suppressed entirely; with the
// full surface (nil probe) the original lines survive.
func TestSelectSurfaceAware(t *testing.T) {
	h := New()
	// Default surface: reduced explore hint, no kern_why.
	got := h.Select("s1", "kern_explore", OutcomeSuccess, onSurface(defaultSurface))
	if got != "next: kern_impact before editing" {
		t.Fatalf("default-surface explore hint = %q, want the kern_impact-only alternative", got)
	}
	// Hints naming only off-surface tools are suppressed, not mangled.
	for _, c := range []struct {
		tool    string
		outcome Outcome
	}{
		{"kern_plan", OutcomeSuccess},   // kern_memory: full-surface only
		{"kern_verify", OutcomeFailure}, // kern_heal: full-surface only
	} {
		if got := h.Select("s2", c.tool, c.outcome, onSurface(defaultSurface)); got != "" {
			t.Errorf("default surface: %s hint must be suppressed, got %q", c.tool, got)
		}
	}
	// kern_search no-match advertises only default-surface tools: kept.
	if got := h.Select("s3", "kern_search", OutcomeNoMatch, onSurface(defaultSurface)); got == "" {
		t.Fatal("default-surface-compliant hint must survive")
	}
	// Full surface (nil probe): the complete original line, kern_why included.
	if got := h.Select("s4", "kern_explore", OutcomeSuccess, nil); got != "next: kern_why names who depends on this; kern_impact before editing" {
		t.Fatalf("full-surface explore hint = %q, want the complete line", got)
	}
}

// TestGapSurfaceAware (F-B4): the usage-gap nudge only names underused tools
// callable on the active surface — on the default surface that is kern_impact
// (semcache/why/plan/memory are full-surface tools), and never "" while a
// ranked on-surface tool is unused.
func TestGapSurfaceAware(t *testing.T) {
	h := New()
	got := h.Gap("s", GapThreshold, map[string]bool{}, onSurface(defaultSurface))
	if !strings.Contains(got, "kern_impact") {
		t.Fatalf("default-surface gap nudge must name kern_impact, got %q", got)
	}
	if strings.Contains(got, "kern_why") || strings.Contains(got, "kern_semcache") {
		t.Fatalf("default-surface gap nudge must not name full-surface tools, got %q", got)
	}
	// Every on-surface ranked tool used → no nudge on the default surface.
	h2 := New()
	if got := h2.Gap("s", GapThreshold, map[string]bool{"kern_impact": true}, onSurface(defaultSurface)); got != "" {
		t.Fatalf("no on-surface candidate must suppress the gap nudge, got %q", got)
	}
	// Full surface: the top-ranked underused tool (kern_semcache) is back.
	h3 := New()
	if got := h3.Gap("s", GapThreshold, map[string]bool{}, nil); !strings.Contains(got, "kern_semcache") {
		t.Fatalf("full-surface gap nudge must name kern_semcache, got %q", got)
	}
}

// TestSelectOncePerSession: a given hint fires at most once per session —
// the second identical selection is empty — while a different session still
// gets it.
func TestSelectOncePerSession(t *testing.T) {
	h := New()
	if got := h.Select("alice", "kern_explore", OutcomeSuccess, nil); got == "" {
		t.Fatal("first selection must fire")
	}
	if got := h.Select("alice", "kern_explore", OutcomeSuccess, nil); got != "" {
		t.Fatalf("a fired hint must not re-fire in the same session, got %q", got)
	}
	// A different session key is untouched.
	if got := h.Select("bob", "kern_explore", OutcomeSuccess, nil); got == "" {
		t.Fatal("a fresh session must still get the hint")
	}
	// A different (tool, outcome) hint is independent.
	if got := h.Select("alice", "kern_plan", OutcomeSuccess, nil); got == "" {
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
	if got := h.Select("s", "kern_explore", OutcomeSuccess, nil); got != "" {
		t.Fatalf("disabled hints must select nothing, got %q", got)
	}
	if got := h.Gap("s", GapThreshold, map[string]bool{}, nil); got != "" {
		t.Fatalf("disabled hints must not gap-nudge, got %q", got)
	}
	// Re-enable: the disabled-era selections were never recorded.
	t.Setenv("KERN_HINTS", "")
	if !h.Enabled() {
		t.Fatal("unset KERN_HINTS must enable hints (default ON)")
	}
	if got := h.Select("s", "kern_explore", OutcomeSuccess, nil); got == "" {
		t.Fatal("a hint not fired while disabled must still fire after re-enabling")
	}
}

// TestGapThresholdAndOncePerTool: the gap hint fires only at/above the
// read-call threshold, names the first ranked underused tool the session has
// not used, and fires at most once per session per tool.
func TestGapThresholdAndOncePerTool(t *testing.T) {
	h := New()
	// Below the threshold: no nudge.
	if got := h.Gap("s", GapThreshold-1, map[string]bool{}, nil); got != "" {
		t.Fatalf("below the threshold must not nudge, got %q", got)
	}
	// At the threshold with nothing used: the most underused tool wins.
	got := h.Gap("s", GapThreshold, map[string]bool{}, nil)
	if !strings.Contains(got, "kern_semcache") {
		t.Fatalf("gap nudge must name the top-ranked underused tool, got %q", got)
	}
	// Once per session per tool: the same tool never re-nudges.
	if got := h.Gap("s", GapThreshold+5, map[string]bool{}, nil); got != "" {
		t.Fatalf("an already-nudged tool must not re-nudge, got %q", got)
	}
	// A fresh session gets its own nudge.
	if got := h.Gap("other", GapThreshold, map[string]bool{}, nil); got == "" || !strings.Contains(got, "kern_semcache") {
		t.Fatalf("a fresh session must get the gap nudge, got %q", got)
	}
}

// TestGapSuppressedWhenToolUsed: a session that already used a top-ranked
// underused tool is nudged toward the NEXT unused one, and a session that
// used every ranked tool gets no nudge at all.
func TestGapSuppressedWhenToolUsed(t *testing.T) {
	h := New()
	// kern_semcache used → the nudge names kern_why instead.
	got := h.Gap("s", GapThreshold, map[string]bool{"kern_semcache": true}, nil)
	if !strings.Contains(got, "kern_why") {
		t.Fatalf("used semcache must advance the nudge to the next underused tool, got %q", got)
	}
	// Every ranked tool used → no nudge.
	all := map[string]bool{"kern_semcache": true, "kern_why": true, "kern_plan": true, "kern_memory": true, "kern_impact": true}
	if got := h.Gap("s2", GapThreshold, all, nil); got != "" {
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
	if got := h.Gap("s", h.Reads("s"), h.Used("s"), nil); got != "" {
		t.Fatalf("below-threshold session must not nudge, got %q", got)
	}
	// 10 reads with kern_memory used → nudge names the next unused tool.
	for i := 0; i < 9; i++ {
		h.Track("s", "kern_search", true)
	}
	got := h.Gap("s", h.Reads("s"), h.Used("s"), nil)
	if !strings.Contains(got, "kern_semcache") {
		t.Fatalf("gap nudge must name the top-ranked unused tool, got %q", got)
	}
}
