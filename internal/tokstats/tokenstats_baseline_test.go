package tokstats

import (
	"strings"
	"testing"
)

// TestTokenStatsSummaryAlwaysLabelsDenominator is the render-level guard for
// honest savings reporting (L2/finding V1): a percentage may never appear
// without its labeled denominator. Every renderer of TokenStats (Summary and
// the HTML TokenStatsPanel) must follow "% … vs <baseline>", including the
// no-savings guard variant ("0% vs …; compact includes metadata").
func TestTokenStatsSummaryAlwaysLabelsDenominator(t *testing.T) {
	cases := []struct {
		name  string
		stats TokenStats
		// noPct is true for stats that must NOT render a % at all (empty
		// full context); every other case must render a labeled %.
		noPct bool
	}{
		{
			name:  "graph multi-file baseline",
			stats: TokenStats{FullContext: 12400, CompactTokens: 380, SavingsPct: 97, Source: "graph", Baseline: "3 files read raw"},
		},
		{
			name:  "context single-file baseline",
			stats: TokenStats{FullContext: 6937, CompactTokens: 353, SavingsPct: 94, Source: "context", Baseline: "file read raw (engine.go)"},
		},
		{
			name:  "no-savings guard keeps note",
			stats: TokenStats{FullContext: 368, CompactTokens: 368, SavingsPct: 0, Source: "explore", Baseline: "verbatim source"},
		},
		{
			name:  "empty baseline falls back to source label",
			stats: TokenStats{FullContext: 100, CompactTokens: 40, SavingsPct: 60, Source: "graph"},
		},
		{
			name:  "empty full context renders nothing",
			stats: TokenStats{FullContext: 0, CompactTokens: 0},
			noPct: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := tc.stats.Summary()
			if tc.noPct {
				if summary != "" {
					t.Errorf("Summary() = %q, want \"\" for empty full context", summary)
				}
				return
			}
			assertPercentHasDenominator(t, "Summary()", summary)
			panel := TokenStatsPanel(tc.stats)
			assertPercentHasDenominator(t, "TokenStatsPanel", panel)
		})
	}
}

// assertPercentHasDenominator fails when a rendered savings line contains a
// "%" but no "vs <label>" clause — the honest-reporting invariant.
func assertPercentHasDenominator(t *testing.T, what, out string) {
	t.Helper()
	if !strings.Contains(out, "%") {
		t.Errorf("%s must report a percentage for this stats, got %q", what, out)
	}
	if !strings.Contains(out, "% vs ") {
		t.Errorf("%s prints a percentage without a labeled denominator: %q", what, out)
	}
	// The label after "vs " must be non-empty and must not be immediately
	// followed by another "%" or a closing paren (a missing label).
	idx := strings.Index(out, "% vs ")
	rest := out[idx+len("% vs "):]
	if rest == "" || rest[0] == '%' || rest[0] == ')' || rest[0] == ';' {
		t.Errorf("%s has an empty denominator label: %q", what, out)
	}
}

// TestTokenStatsSummaryFormats pins the exact rendered shape on a real
// example, so a future format change is a deliberate, reviewed decision.
func TestTokenStatsSummaryFormats(t *testing.T) {
	got := (TokenStats{
		FullContext: 12400, CompactTokens: 380, SavingsPct: 97,
		Source: "graph", Baseline: "3 files read raw",
	}).Summary()
	want := "tokens: graph 12400 → 380 (97% vs 3 files read raw)"
	if got != want {
		t.Errorf("savings summary = %q, want %q", got, want)
	}
	gotNoop := (TokenStats{
		FullContext: 368, CompactTokens: 368, SavingsPct: 0,
		Source: "explore", Baseline: "verbatim source",
	}).Summary()
	wantNoop := "tokens: explore 368 → 368 (0% vs verbatim source; compact includes metadata)"
	if gotNoop != wantNoop {
		t.Errorf("no-op summary = %q, want %q", gotNoop, wantNoop)
	}
}
