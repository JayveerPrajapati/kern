package whatif

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
)

// enhanceFixtureGraph builds a hand-crafted knowledge graph for the enhanced
// what-if dimensions:
//   - A calls B, B calls C (a call chain),
//   - D calls A, so D depends on B transitively but does NOT call it directly,
//   - test T calls C, so T is the covering test for C.
//
// Node IDs are the bare symbol names; edges are "calls" edges only, which is
// what WhatDependsOn/WhatTestsCover traverse.
func enhanceFixtureGraph() *intelligence.Graph {
	g := &intelligence.Graph{}
	sym := func(id, name, file string) domain.Node {
		return domain.Node{
			ID:     id,
			Kind:   "symbol",
			Label:  id,
			Symbol: &domain.Symbol{Name: name, Qualified: "pkg." + name, File: file},
		}
	}
	g.Nodes = []domain.Node{
		sym("A", "A", "a.go"),
		sym("B", "B", "b.go"),
		sym("C", "C", "c.go"),
		sym("D", "D", "d.go"),
		sym("T", "TestC", "c_test.go"),
	}
	g.Edges = []domain.Edge{
		{From: "A", To: "B", Kind: "calls"},
		{From: "B", To: "C", Kind: "calls"},
		{From: "D", To: "A", Kind: "calls"},
		{From: "T", To: "C", Kind: "calls"},
	}
	return g
}

// findClaim returns the first claim whose type and statement substring match.
func findClaim(imp Impact, claimType domain.ClaimType, substr string) (domain.Claim, bool) {
	for _, c := range imp.Claims {
		if c.Type == claimType && strings.Contains(c.Statement, substr) {
			return c, true
		}
	}
	return domain.Claim{}, false
}

func TestSimulateChangeSignatureFlagsDirectCallersOnly(t *testing.T) {
	imp := Simulate(enhanceFixtureGraph(), Change{Kind: ChangeSignature, Target: "B"})
	want := []string{"A"}
	if !reflect.DeepEqual(imp.BrokenCallSites, want) {
		t.Fatalf("BrokenCallSites = %v, want %v (only direct callers; NOT D, NOT C)", imp.BrokenCallSites, want)
	}
	c, ok := findClaim(imp, domain.ClaimInference, "direct call site")
	if !ok {
		t.Fatal("expected an INFERENCE claim about broken direct call sites")
	}
	if !strings.Contains(c.Statement, "1 direct call site") {
		t.Fatalf("claim statement %q should report exactly 1 direct call site", c.Statement)
	}
	if c.Provenance != "direct-call-edge scan of the knowledge graph" {
		t.Fatalf("claim provenance = %q", c.Provenance)
	}
}

func TestSimulateRenameSymbolFlagsDirectCallers(t *testing.T) {
	imp := Simulate(enhanceFixtureGraph(), Change{Kind: RenameSymbol, Target: "B"})
	want := []string{"A"}
	if !reflect.DeepEqual(imp.BrokenCallSites, want) {
		t.Fatalf("BrokenCallSites = %v, want %v", imp.BrokenCallSites, want)
	}
	if _, ok := findClaim(imp, domain.ClaimInference, "direct call site"); !ok {
		t.Fatal("expected a broken-call-site claim for rename_symbol")
	}
}

func TestSimulateRemoveSymbolNoBrokenCallSites(t *testing.T) {
	imp := Simulate(enhanceFixtureGraph(), Change{Kind: RemoveSymbol, Target: "B"})
	if len(imp.BrokenCallSites) != 0 {
		t.Fatalf("BrokenCallSites = %v, want empty/nil for remove_symbol", imp.BrokenCallSites)
	}
	if _, ok := findClaim(imp, domain.ClaimInference, "direct call site"); ok {
		t.Fatal("remove_symbol must not emit a broken-call-site claim")
	}
}

func TestSimulateUntestedAffected(t *testing.T) {
	imp := Simulate(enhanceFixtureGraph(), Change{Kind: RemoveSymbol, Target: "C"})
	// C is covered by test T, so it must NOT be untested; A and B have no
	// covering tests. D depends on C transitively (D -> A -> B -> C) and is
	// also untested.
	if contains(imp.UntestedAffected, "C") {
		t.Fatalf("C must not be untested (covered by T); got %v", imp.UntestedAffected)
	}
	if !contains(imp.UntestedAffected, "A") || !contains(imp.UntestedAffected, "B") {
		t.Fatalf("A and B must be listed as untested; got %v", imp.UntestedAffected)
	}
	if !sort.StringsAreSorted(imp.UntestedAffected) {
		t.Fatalf("UntestedAffected must be sorted ascending; got %v", imp.UntestedAffected)
	}
	// T itself calls C and is affected (nothing calls T, so nothing covers
	// it); the exact set is deterministic: [A B D T], C excluded.
	want := []string{"A", "B", "D", "T"}
	if !reflect.DeepEqual(imp.UntestedAffected, want) {
		t.Fatalf("UntestedAffected = %v, want %v (C covered by T)", imp.UntestedAffected, want)
	}
	if c, ok := findClaim(imp, domain.ClaimInference, "no covering tests"); !ok {
		t.Fatal("expected an INFERENCE claim about symbols without covering tests")
	} else if !strings.Contains(c.Statement, "4 affected symbol(s) have no covering tests") {
		t.Fatalf("claim statement = %q", c.Statement)
	}
}

func TestSimulateDeterministicNewFields(t *testing.T) {
	g := enhanceFixtureGraph()
	a := Simulate(g, Change{Kind: ChangeSignature, Target: "B"})
	b := Simulate(g, Change{Kind: ChangeSignature, Target: "B"})
	if !reflect.DeepEqual(a.BrokenCallSites, b.BrokenCallSites) {
		t.Fatalf("BrokenCallSites differ across runs: %v vs %v", a.BrokenCallSites, b.BrokenCallSites)
	}
	if !reflect.DeepEqual(a.UntestedAffected, b.UntestedAffected) {
		t.Fatalf("UntestedAffected differ across runs: %v vs %v", a.UntestedAffected, b.UntestedAffected)
	}
}
