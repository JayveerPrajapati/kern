package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func dupNameFixture() *index.Index {
	syms := []index.Symbol{
		{Kind: "func", Name: "Load", File: "a/a.go", Line: 10},
		{Kind: "func", Name: "UseA", File: "a/a.go", Line: 20},
		{Kind: "func", Name: "Solo", File: "a/a.go", Line: 30},
		{Kind: "func", Name: "Load", File: "b/b.go", Line: 10},
		{Kind: "func", Name: "UseB", File: "b/b.go", Line: 20},
		{Kind: "func", Name: "UseBoth", File: "c/c.go", Line: 10},
		{Kind: "func", Name: "Orphan", File: "d/d.go", Line: 10},
		{Kind: "func", Name: "UseB2", File: "e/e.go", Line: 10},
	}
	edge := func(target string) []index.CallEdge {
		return []index.CallEdge{{Target: target, Confidence: index.ConfidenceHigh}}
	}
	return &index.Index{
		Symbols: syms,
		Calls: map[string][]index.CallEdge{
			"UseA":    edge("Load"),
			"UseB":    edge("Load"),
			"UseBoth": edge("Load"),
			"Orphan":  edge("Load"),
			"UseB2":   edge("Load"),
		},
		Callers: map[string][]string{
			"Load":    {"UseA", "UseB", "UseBoth", "Orphan", "UseB2"},
			"Solo":    {"UseA"},
			"UseA":    {},
			"UseB":    {},
			"UseBoth": {},
			"Orphan":  {},
			"UseB2":   {},
		},
		ImportsByFile: map[string][]index.ImportEdge{
			"c/c.go": {{Path: "example.com/proj/a", Confidence: index.ConfidenceHigh}},
			"e/e.go": {{Path: "example.com/proj/b", Confidence: index.ConfidenceHigh}},
		},
	}
}

func bridgeBySymbol(bridges []Bridge, sym string) *Bridge {
	for i := range bridges {
		if bridges[i].Symbol == sym {
			return &bridges[i]
		}
	}
	return nil
}

func TestBridgesQualifyAmbiguousNames(t *testing.T) {
	ix := dupNameFixture()
	bridges := Bridges(ix, 10)
	for _, b := range bridges {
		if b.Symbol == "Load" {
			t.Fatalf("bare ambiguous row must not appear, got %+v (all bridges: %+v)", b, bridges)
		}
	}
	a := bridgeBySymbol(bridges, "a.Load")
	b := bridgeBySymbol(bridges, "b.Load")
	if a == nil || b == nil {
		t.Fatalf("expected a.Load and b.Load rows, got %+v", bridges)
	}
	// a.Load: same-package UseA + import-attributed UseBoth, across dirs a, c.
	if a.Callers != 2 || len(a.Packages) != 2 {
		t.Errorf("a.Load = %d callers across %v, want 2 callers across [a c]", a.Callers, a.Packages)
	}
	// b.Load: same-package UseB + import-attributed UseB2, across dirs b, e.
	if b.Callers != 2 || len(b.Packages) != 2 {
		t.Errorf("b.Load = %d callers across %v, want 2 callers across [b e]", b.Callers, b.Packages)
	}
	// Orphan is attributable to no definition: it must be dropped, not
	// credited to both (2+2=4 < 5 bucket entries).
	if a.Callers+b.Callers != 4 {
		t.Errorf("attributed callers = %d, want 4 (Orphan dropped as ambiguous)", a.Callers+b.Callers)
	}
}

func TestHubsQualifyAmbiguousNames(t *testing.T) {
	ix := dupNameFixture()
	hubs := Hubs(ix, 20)
	bySym := map[string]Hub{}
	for _, h := range hubs {
		if _, dup := bySym[h.Symbol]; dup {
			t.Fatalf("duplicate hub row %q, hubs: %+v", h.Symbol, hubs)
		}
		bySym[h.Symbol] = h
	}
	if _, bare := bySym["Load"]; bare {
		t.Fatalf("bare ambiguous hub row must not appear, hubs: %+v", hubs)
	}
	a, okA := bySym["a.Load"]
	b, okB := bySym["b.Load"]
	if !okA || !okB {
		t.Fatalf("expected a.Load and b.Load hub rows, got %+v", hubs)
	}
	if a.Callers != 2 || b.Callers != 2 {
		t.Errorf("a.Load=%d b.Load=%d callers, want 2 each", a.Callers, b.Callers)
	}
	if a.File != "a/a.go" || b.File != "b/b.go" {
		t.Errorf("rows must cite their own definition file, got %s and %s", a.File, b.File)
	}
	solo, ok := bySym["Solo"]
	if !ok {
		t.Fatalf("unique names keep bare rows, hubs: %+v", hubs)
	}
	if solo.Callers != 1 {
		t.Errorf("Solo = %d callers, want 1 (aggregated path unchanged)", solo.Callers)
	}
}

// TestHubSetKeepsBareKeys pins the risk-scoring contract: hubSet carries both
// the qualified row key and the bare FullName, so changes.go/wiki.go lookups
// by bare name keep working (conservative, as before).
func TestHubSetKeepsBareKeys(t *testing.T) {
	ix := dupNameFixture()
	set := hubSet(ix)
	if !set["a.Load"] {
		t.Errorf("hubSet must contain the qualified row key a.Load, got %v", set)
	}
	if !set["Load"] {
		t.Errorf("hubSet must keep the bare key Load for risk lookups, got %v", set)
	}
}

func TestCommunityHubQualifiesAmbiguousNames(t *testing.T) {
	ix := dupNameFixture()
	comms := renderCommunities(ix, map[string]string{
		"Load": "g", "UseA": "g", "UseB": "g", "UseBoth": "g",
		"Solo": "g", "Orphan": "g", "UseB2": "g",
	})
	if len(comms) != 1 {
		t.Fatalf("expected one community, got %+v", comms)
	}
	if comms[0].Hub != "a.Load" {
		t.Errorf("community hub = %q, want a.Load (2 attributed callers beat b.Load)", comms[0].Hub)
	}
}
