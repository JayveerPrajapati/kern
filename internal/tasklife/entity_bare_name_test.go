package tasklife

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Impact reports carry bare names. A bare name defined in several packages
// (New, Save, Generate) must not pool its siblings' entities into the overlay.
func TestEntityOverlayIgnoresAmbiguousBareNames(t *testing.T) {
	g := &intel.Graph{Graph: domain.Graph{
		Nodes: []domain.Node{
			{ID: "pkg1.Save", Kind: "symbol", Label: "Save", Symbol: &domain.Symbol{Name: "Save", Qualified: "Save", File: "pkg1/a.go"}},
			{ID: "pkg2.Save", Kind: "symbol", Label: "Save", Symbol: &domain.Symbol{Name: "Save", Qualified: "Save", File: "pkg2/b.go"}},
			{ID: "pkg2.Only", Kind: "symbol", Label: "Only", Symbol: &domain.Symbol{Name: "Only", Qualified: "Only", File: "pkg2/b.go"}},
			{ID: "svc:POST:/orders", Kind: "api", Label: "POST /orders", API: &domain.API{Name: "POST /orders", File: "pkg2/routes.go"}},
		},
		Edges: []domain.Edge{
			{From: "svc:POST:/orders", To: "pkg2.Save", Kind: "implements"},
			{From: "svc:POST:/orders", To: "pkg2.Only", Kind: "implements"},
		},
	}}
	if ents := attachImpactEntities(g, []string{"Save"}); len(ents) != 0 {
		t.Fatalf("ambiguous bare name Save must implicate no entities: %v", ents)
	}
	ents := attachImpactEntities(g, []string{"Only"})
	if len(ents) != 1 || ents[0].Name != "POST /orders" {
		t.Fatalf("unique bare name Only must keep its entity: %v", ents)
	}
}
