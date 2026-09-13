package index

import (
	"sort"
	"testing"
)

// TestDedupeKeepsHighestConfidence: dedupeCallEdges must keep the
// HIGHEST-confidence representative for a target — an edge recorded LOW
// before it was re-resolved must not stay LOW forever (CG-P1-6).
func TestDedupeKeepsHighestConfidence(t *testing.T) {
	cases := []struct {
		name string
		in   []CallEdge
		want Confidence
	}{
		{
			name: "low-then-high",
			in: []CallEdge{
				{Target: "T", Confidence: ConfidenceLow},
				{Target: "T", Confidence: ConfidenceHigh},
			},
			want: ConfidenceHigh,
		},
		{
			name: "high-then-low",
			in: []CallEdge{
				{Target: "T", Confidence: ConfidenceHigh},
				{Target: "T", Confidence: ConfidenceLow},
			},
			want: ConfidenceHigh,
		},
		{
			name: "low-medium-high",
			in: []CallEdge{
				{Target: "T", Confidence: ConfidenceLow},
				{Target: "T", Confidence: ConfidenceMedium},
				{Target: "T", Confidence: ConfidenceHigh},
			},
			want: ConfidenceHigh,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := dedupeCallEdges(tc.in)
			if len(out) != 1 {
				t.Fatalf("dedupeCallEdges(%v) = %v; want exactly 1 edge", tc.in, out)
			}
			if out[0].Target != "T" || out[0].Confidence != tc.want {
				t.Errorf("kept %+v; want {T %s}", out[0], tc.want)
			}
		})
	}
}

// TestDedupeTieKeepsFirst: for equal confidence the FIRST occurrence wins,
// preserving input order determinism. Synth distinguishes the two edges.
func TestDedupeTieKeepsFirst(t *testing.T) {
	in := []CallEdge{
		{Target: "T", Confidence: ConfidenceHigh, Synth: "router:first"},
		{Target: "T", Confidence: ConfidenceHigh, Synth: "router:second"},
	}
	out := dedupeCallEdges(in)
	if len(out) != 1 {
		t.Fatalf("dedupeCallEdges(%v) = %v; want exactly 1 edge", in, out)
	}
	if out[0].Synth != "router:first" {
		t.Errorf("tie kept %+v; want the first occurrence (Synth router:first)", out[0])
	}
}

// TestDedupeSortedByTarget: the deduped output is sorted by target so the
// Calls map stays in the deterministic order finalize passes rely on.
func TestDedupeSortedByTarget(t *testing.T) {
	in := []CallEdge{
		{Target: "zebra", Confidence: ConfidenceHigh},
		{Target: "alpha", Confidence: ConfidenceMedium},
		{Target: "mid", Confidence: ConfidenceLow},
	}
	out := dedupeCallEdges(in)
	if !sort.SliceIsSorted(out, func(i, j int) bool { return out[i].Target < out[j].Target }) {
		t.Fatalf("output not sorted by target: %v", out)
	}
	want := []string{"alpha", "mid", "zebra"}
	if len(out) != len(want) {
		t.Fatalf("got %d edges, want %d: %v", len(out), len(want), out)
	}
	for i, w := range want {
		if out[i].Target != w {
			t.Errorf("out[%d].Target = %q; want %q (full: %v)", i, out[i].Target, w, out)
		}
	}
}

// TestDedupePromotionPathStillWorks: a direct check of the promote-style
// input — the promotion pass canonicalizes distinct target forms ("db.Open"
// and "Open") into one key and re-dedupes; the merged key must keep the
// highest confidence. The full promotion path itself is covered by
// promote_test.go (TestPromoteLowEdgesDedupeKeepsBestConfidence) and runs
// with this package.
func TestDedupePromotionPathStillWorks(t *testing.T) {
	// "db.Open" canonicalized to "Open" and merged with an existing "Open"
	// edge: the surviving representative carries the highest confidence.
	in := []CallEdge{
		{Target: "Open", Confidence: ConfidenceMedium},
		{Target: "Open", Confidence: ConfidenceLow},
	}
	out := dedupeCallEdges(in)
	if len(out) != 1 {
		t.Fatalf("dedupeCallEdges(%v) = %v; want exactly 1 edge", in, out)
	}
	if out[0].Target != "Open" || out[0].Confidence != ConfidenceMedium {
		t.Errorf("promote-style dedupe kept %+v; want {Open MEDIUM}", out[0])
	}
}
