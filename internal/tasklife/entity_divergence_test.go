package tasklife

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// TestEntityOverlayDivergesForUnrelatedSymbols (2026-10-03 findings):
// the "Affected entities" overlay used to return IDENTICAL lists for two
// unrelated symbols because it was fed the over-counted blast-radius set
// (every hub name pooled cross-package callers, converging both sets onto the
// same packages, and twin's package-service expansion mapped any symbol in a
// package to that package's service entity). With the ambiguous-name
// amplification removed, each symbol's overlay must reflect only its
// own genuine reach — two unrelated symbols must NOT produce the same
// entity list.
func TestEntityOverlayDivergesForUnrelatedSymbols(t *testing.T) {
	g := &intel.Graph{Graph: domain.Graph{
		Nodes: []domain.Node{
			// Two services, each with an API entity wired to its own handler.
			{ID: "api.HandlerA", Kind: "symbol", Label: "HandlerA", Symbol: &domain.Symbol{Name: "HandlerA", Qualified: "api.HandlerA", File: "api/a.go"}},
			{ID: "svc.HandlerB", Kind: "symbol", Label: "HandlerB", Symbol: &domain.Symbol{Name: "HandlerB", Qualified: "svc.HandlerB", File: "svc/b.go"}},
			{ID: "api:gin:GET:/users", Kind: "api", Label: "GET /users", API: &domain.API{Name: "GET /users", File: "api/routes.go"}},
			{ID: "svc:POST:/orders", Kind: "api", Label: "POST /orders", API: &domain.API{Name: "POST /orders", File: "svc/routes.go"}},
		},
		Edges: []domain.Edge{
			{From: "api:gin:GET:/users", To: "api.HandlerA", Kind: "implements"},
			{From: "svc:POST:/orders", To: "svc.HandlerB", Kind: "implements"},
		},
	}}
	entsA := attachImpactEntities(g, []string{"api.HandlerA"})
	entsB := attachImpactEntities(g, []string{"svc.HandlerB"})
	if len(entsA) == 0 || len(entsB) == 0 {
		t.Fatalf("both symbols must implicate their own entity: A=%v B=%v", entsA, entsB)
	}
	if entsA[0].Name == entsB[0].Name {
		t.Fatalf("unrelated symbols produced the same entity list: %v vs %v", entsA, entsB)
	}
	if entsA[0].Name != "GET /users" || entsB[0].Name != "POST /orders" {
		t.Fatalf("entity attribution crossed packages: A=%v B=%v", entsA, entsB)
	}
	// A symbol with no twin connections must stay entity-free even when its
	// simple name is a hub that other packages also define (the old failure
	// mode: hub-name amplification dragged every package into the overlay).
	hub := &intel.Graph{Graph: domain.Graph{
		Nodes: []domain.Node{
			{ID: "pkg1.Save", Kind: "symbol", Label: "Save", Symbol: &domain.Symbol{Name: "Save", Qualified: "pkg1.Save", File: "pkg1/a.go"}},
			{ID: "pkg2.Save", Kind: "symbol", Label: "Save", Symbol: &domain.Symbol{Name: "Save", Qualified: "pkg2.Save", File: "pkg2/b.go"}},
			{ID: "svc:POST:/orders", Kind: "api", Label: "POST /orders", API: &domain.API{Name: "POST /orders", File: "pkg2/routes.go"}},
		},
		Edges: []domain.Edge{
			{From: "svc:POST:/orders", To: "pkg2.Save", Kind: "implements"},
		},
	}}
	if ents := attachImpactEntities(hub, []string{"pkg1.Save"}); len(ents) != 0 {
		t.Fatalf("pkg1.Save must implicate no entities (its same-named sibling pkg2.Save owns the route): %v", ents)
	}
}
