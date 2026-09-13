package intel

import (
	"fmt"
	"math"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// hubsFixture builds a synthetic index with two communities and two hub
// candidates (A1, B1) that have EQUAL raw scores (1 caller, 0 callees -> raw
// 2) but live in communities of very different sizes:
//
//	community "a": 2 symbols  (A1, A2)
//	community "b": 50 symbols (B1..B50)
//
// A2 -> A1 and B2 -> B1 give both candidates one caller; every other symbol
// has one callee and no callers (raw 1), so no filler outranks a candidate.
// With community normalization A1 (raw 2 / sqrt(2) ~= 1.41) must outrank B1
// (raw 2 / sqrt(50) ~= 0.28) despite identical raw scores. The fixture is
// deliberately synthetic (mirroring intel_planned_test.go's hand-built
// indexes) so community sizes and raw scores are exact.
func hubsFixture() *index.Index {
	var syms []index.Symbol
	communities := map[string]string{}
	calls := map[string][]index.CallEdge{}
	callers := map[string][]string{}

	// Community "a": A1 (candidate, called by A2), A2 (filler).
	syms = append(syms,
		index.Symbol{Kind: "func", Name: "A1", File: "a/a.go", Line: 10},
		index.Symbol{Kind: "func", Name: "A2", File: "a/a.go", Line: 20},
	)
	communities["A1"] = "a"
	communities["A2"] = "a"
	calls["A2"] = []index.CallEdge{{Target: "A1", Confidence: index.ConfidenceHigh}}
	callers["A1"] = []string{"A2"}

	// Community "b": B1 (candidate, called by B2), B2..B50 (fillers). Every
	// filler carries one callee so it stays a hub candidate; the callees point
	// at BFiller, a struct-kind symbol that Hubs skips (only func/method are
	// ranked), so community "b" keeps exactly 50 symbols.
	syms = append(syms,
		index.Symbol{Kind: "func", Name: "B1", File: "b/b.go", Line: 10},
		index.Symbol{Kind: "struct", Name: "BFiller", File: "b/b.go", Line: 999},
	)
	communities["B1"] = "b"
	for i := 2; i <= 50; i++ {
		name := fmt.Sprintf("B%d", i)
		syms = append(syms, index.Symbol{Kind: "func", Name: name, File: "b/b.go", Line: i * 10})
		communities[name] = "b"
		if i == 2 {
			calls[name] = []index.CallEdge{{Target: "B1", Confidence: index.ConfidenceHigh}}
		} else {
			calls[name] = []index.CallEdge{{Target: "BFiller", Confidence: index.ConfidenceHigh}}
		}
	}
	callers["B1"] = []string{"B2"}

	return &index.Index{
		Symbols:     syms,
		Calls:       calls,
		Callers:     callers,
		Communities: communities,
	}
}

// TestHubCommunityNormalization: with Communities populated, the candidate in
// the 2-symbol community outranks the candidate with the SAME raw score in the
// 50-symbol community, and every Weighted value is exactly raw/sqrt(commSize).
func TestHubCommunityNormalization(t *testing.T) {
	ix := hubsFixture()
	hubs := Hubs(ix, 52)
	if len(hubs) != 52 {
		t.Fatalf("expected 52 hubs, got %d", len(hubs))
	}
	var a1, b1 Hub
	for _, h := range hubs {
		switch h.Symbol {
		case "A1":
			a1 = h
		case "B1":
			b1 = h
		}
	}
	if a1.Symbol == "" || b1.Symbol == "" {
		t.Fatal("expected A1 and B1 to be hub candidates")
	}
	if a1.Score != b1.Score {
		t.Fatalf("fixture must give candidates equal raw scores: A1=%d B1=%d", a1.Score, b1.Score)
	}
	// Normalized scores are exactly raw / sqrt(community size).
	wantA := float64(a1.Score) / math.Sqrt(2)
	wantB := float64(b1.Score) / math.Sqrt(50)
	if math.Abs(a1.Weighted-wantA) > 1e-9 {
		t.Errorf("A1 weighted = %v, want %v", a1.Weighted, wantA)
	}
	if math.Abs(b1.Weighted-wantB) > 1e-9 {
		t.Errorf("B1 weighted = %v, want %v", b1.Weighted, wantB)
	}
	// The small-community candidate ranks first despite equal raw.
	if hubs[0].Symbol != "A1" {
		t.Errorf("expected A1 (2-symbol community) to rank first, got %+v", hubs[0])
	}
	if a1.Weighted <= b1.Weighted {
		t.Errorf("A1 weighted %v must exceed B1 weighted %v", a1.Weighted, b1.Weighted)
	}
	// Full normalized order: A1, A2, B1, then the raw-1 B fillers sorted by
	// name (lexicographic: B10, B11, ... come before B2).
	wantOrder := []string{"A1", "A2", "B1", "B10", "B11"}
	for i, want := range wantOrder {
		if hubs[i].Symbol != want {
			t.Errorf("position %d: got %s, want %s", i, hubs[i].Symbol, want)
		}
	}
}

// TestHubFallbackWithoutCommunities: when Communities is nil/empty (indexes
// that never computed them), Weighted equals the raw Score for every hub and
// the ranking is unchanged from the raw ordering (raw desc, name asc on ties).
func TestHubFallbackWithoutCommunities(t *testing.T) {
	ix := hubsFixture()
	ix.Communities = nil
	hubs := Hubs(ix, 52)
	if len(hubs) != 52 {
		t.Fatalf("expected 52 hubs, got %d", len(hubs))
	}
	for _, h := range hubs {
		if h.Weighted != float64(h.Score) {
			t.Errorf("%s: weighted %v != raw %d when Communities is empty", h.Symbol, h.Weighted, h.Score)
		}
	}
	// Raw tie between A1 and B1 (both 2) falls back to name-asc tie-break;
	// raw order is A1, B1, then the raw-1 symbols by name (lexicographic).
	wantOrder := []string{"A1", "B1", "A2", "B10", "B11"}
	for i, want := range wantOrder {
		if hubs[i].Symbol != want {
			t.Errorf("position %d: got %s, want %s (raw order)", i, hubs[i].Symbol, want)
		}
	}
}

// tieBreakFixture builds an index with two communities where Y1 and X1 share
// the SAME weighted score via different raw/community pairs, exercising the
// raw-desc tie-break; X2/X3/X4 then share weighted AND raw, exercising the
// name-asc tie-break.
//
//	community "x" (4 symbols): X1 raw 2 -> weighted 2/sqrt(4) = 1.0;
//	                          X2, X3, X4 raw 1 -> weighted 0.5
//	community "y" (9 symbols): Y1 raw 3 -> weighted 3/sqrt(9) = 1.0;
//	                          Y2..Y9 raw 1 -> weighted ~0.33
func tieBreakFixture() *index.Index {
	var syms []index.Symbol
	communities := map[string]string{}
	calls := map[string][]index.CallEdge{}
	callers := map[string][]string{}

	syms = append(syms,
		index.Symbol{Kind: "func", Name: "X1", File: "x/x.go", Line: 10},
		index.Symbol{Kind: "func", Name: "X2", File: "x/x.go", Line: 20},
		index.Symbol{Kind: "func", Name: "X3", File: "x/x.go", Line: 30},
		index.Symbol{Kind: "func", Name: "X4", File: "x/x.go", Line: 40},
	)
	for _, n := range []string{"X1", "X2", "X3", "X4"} {
		communities[n] = "x"
	}
	calls["X2"] = []index.CallEdge{{Target: "X1", Confidence: index.ConfidenceHigh}}
	callers["X1"] = []string{"X2"} // X1: 1 caller, 0 callees -> raw 2

	syms = append(syms, index.Symbol{Kind: "func", Name: "Y1", File: "y/y.go", Line: 10})
	communities["Y1"] = "y"
	for i := 2; i <= 9; i++ {
		name := fmt.Sprintf("Y%d", i)
		syms = append(syms, index.Symbol{Kind: "func", Name: name, File: "y/y.go", Line: i * 10})
		communities[name] = "y"
	}
	calls["Y2"] = []index.CallEdge{{Target: "Y1", Confidence: index.ConfidenceHigh}}
	callers["Y1"] = []string{"Y2"}
	callers["Y5"] = []string{"Y1"}
	calls["Y1"] = []index.CallEdge{{Target: "Y5", Confidence: index.ConfidenceHigh}} // Y1: 1 caller + 1 callee -> raw 3
	// Y3, Y4, Y6..Y9 carry one callee each (to the unlabeled struct YFiller,
	// which Hubs skips) so they stay raw-1 hub candidates.
	syms = append(syms, index.Symbol{Kind: "struct", Name: "YFiller", File: "y/y.go", Line: 999})
	for _, n := range []string{"Y3", "Y4", "Y6", "Y7", "Y8", "Y9"} {
		calls[n] = []index.CallEdge{{Target: "YFiller", Confidence: index.ConfidenceHigh}}
	}
	// X3, X4 carry one callee each (to the unlabeled struct XFiller) so they
	// stay raw-1 hub candidates.
	syms = append(syms, index.Symbol{Kind: "struct", Name: "XFiller", File: "x/x.go", Line: 999})
	for _, n := range []string{"X3", "X4"} {
		calls[n] = []index.CallEdge{{Target: "XFiller", Confidence: index.ConfidenceHigh}}
	}

	return &index.Index{
		Symbols:     syms,
		Calls:       calls,
		Callers:     callers,
		Communities: communities,
	}
}

// TestHubSortTieBreakChain: weighted desc, then raw desc, then name asc.
// Y1 and X1 have equal weighted (1.0) but Y1's raw (3) exceeds X1's (2), so
// raw desc puts Y1 first; X2/X3/X4 tie on weighted AND raw, so name asc.
func TestHubSortTieBreakChain(t *testing.T) {
	ix := tieBreakFixture()
	hubs := Hubs(ix, 13)
	if len(hubs) != 13 {
		t.Fatalf("expected 13 hubs, got %d", len(hubs))
	}
	if hubs[0].Symbol != "Y1" {
		t.Errorf("position 0: got %s, want Y1 (raw-desc wins the weighted tie)", hubs[0].Symbol)
	}
	if hubs[1].Symbol != "X1" {
		t.Errorf("position 1: got %s, want X1", hubs[1].Symbol)
	}
	if math.Abs(hubs[0].Weighted-hubs[1].Weighted) > 1e-9 {
		t.Errorf("expected Y1 and X1 to tie on weighted, got %v vs %v", hubs[0].Weighted, hubs[1].Weighted)
	}
	if hubs[0].Score <= hubs[1].Score {
		t.Errorf("expected raw-desc tie-break: Y1 raw %d > X1 raw %d", hubs[0].Score, hubs[1].Score)
	}
	// X2/X3/X4 tie on weighted AND raw -> name asc, consecutive.
	pos := map[string]int{}
	for i, h := range hubs {
		pos[h.Symbol] = i
	}
	if pos["X2"] > pos["X3"] || pos["X3"] > pos["X4"] {
		t.Errorf("name-asc tie-break violated: X2@%d X3@%d X4@%d", pos["X2"], pos["X3"], pos["X4"])
	}
}

// TestHubSortDeterminism: repeated Hubs calls over the same index produce
// identical output (sort is fully deterministic, no map-iteration leakage).
func TestHubSortDeterminism(t *testing.T) {
	ix := hubsFixture()
	first := Hubs(ix, 52)
	second := Hubs(ix, 52)
	if len(first) != len(second) {
		t.Fatalf("determinism: lengths differ %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Symbol != second[i].Symbol {
			t.Fatalf("non-deterministic ordering at %d: %s vs %s", i, first[i].Symbol, second[i].Symbol)
		}
	}
}

// TestHubSetUsesWeightedRanking: hubSet must be exactly the top-K of Hubs'
// community-weighted ranking, so wiki hub flags stay consistent with kern hubs
// output. With the two-community fixture the weighted top-5 is
// {A1, A2, B1, B10, B11} — A2 (raw 1, weighted 0.71) outranks B1 (raw 2,
// weighted 0.28), which a raw-only ranking would never reflect.
func TestHubSetUsesWeightedRanking(t *testing.T) {
	ix := hubsFixture()
	set := hubSet(ix)
	ranked := Hubs(ix, 0)
	if len(ranked) == 0 {
		t.Fatal("expected hubs")
	}
	k := max(len(ranked)/10, min(len(ranked), 5))
	if len(set) != k {
		t.Fatalf("hubSet size = %d, want top %d", len(set), k)
	}
	for _, h := range ranked[:k] {
		if !set[h.Symbol] {
			t.Errorf("hubSet missing %q from the weighted top %d", h.Symbol, k)
		}
	}
	want := map[string]bool{"A1": true, "A2": true, "B1": true, "B10": true, "B11": true}
	if len(set) != len(want) {
		t.Fatalf("hubSet = %v, want %v", set, want)
	}
	for s := range want {
		if !set[s] {
			t.Errorf("hubSet missing %q, want the weighted top-5 %v", s, want)
		}
	}
}
