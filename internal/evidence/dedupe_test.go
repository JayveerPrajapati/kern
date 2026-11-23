package evidence

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// A fresh export carried 31 duplicate from→to pairs (2026-11-10 campaign);
// the bundle must record each authorized edge once.
func TestDedupeScopeCollapsesDuplicateEdges(t *testing.T) {
	scope := governance.AuthorizedScope{
		Symbols: []governance.SymbolRef{
			{Name: "A", Qualified: "p.A", File: "p/a.go", Line: 1},
			{Name: "A", Qualified: "p.A", File: "p/a.go", Line: 1},
			{Name: "B", Qualified: "p.B", File: "p/b.go", Line: 2},
		},
		Edges: []governance.EdgeRef{
			{From: "A", To: "B"},
			{From: "A", To: "B"},
			{From: "A", To: "B"},
			{From: "B", To: "A"},
		},
	}
	got := dedupeScope(scope)
	if len(got.Symbols) != 2 {
		t.Fatalf("symbols = %d, want 2", len(got.Symbols))
	}
	if len(got.Edges) != 2 {
		t.Fatalf("edges = %d, want 2 (A→B once, B→A once)", len(got.Edges))
	}
	if got.Edges[0] != (governance.EdgeRef{From: "A", To: "B"}) {
		t.Fatalf("first edge = %+v, want A→B (order preserved)", got.Edges[0])
	}
}
