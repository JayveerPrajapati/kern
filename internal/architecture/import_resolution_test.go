package architecture

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestCollectEdgesUsesImportToDisambiguateBasenames pins the same-basename misattribution: two packages
// with the same basename ("a/util" and "b/util") and the same symbol name
// must not be resolved by lexicographic accident. The caller imports b/util,
// so the edge must point at b/util.
func TestCollectEdgesUsesImportToDisambiguateBasenames(t *testing.T) {
	aSym := index.Symbol{Name: "Helper", Kind: "func", File: "a/util/util.go", Line: 1}
	bSym := index.Symbol{Name: "Helper", Kind: "func", File: "b/util/util.go", Line: 2}
	mainSym := index.Symbol{Name: "Run", Kind: "func", File: "app/main.go", Line: 1}
	ix := &index.Index{
		Root: "/repo",
		Symbols: []index.Symbol{
			aSym, bSym, mainSym,
		},
		SymbolsByFile: map[string][]index.Symbol{
			"a/util/util.go": {aSym},
			"b/util/util.go": {bSym},
			"app/main.go":    {mainSym},
		},
		Calls:   map[string][]index.CallEdge{"Run": {{Target: "util.Helper"}}},
		Callers: map[string][]string{},
		Pkgs: map[string]*index.Pkg{
			"a/util": {Name: "util"},
			"b/util": {Name: "util"},
			"app":    {Name: "main"},
		},
		ImportsByFile: map[string][]index.ImportEdge{
			"app/main.go": {{Path: "example.com/x/b/util"}},
		},
	}
	edges := collectEdges(ix, []string{"app/main.go"})
	if len(edges) != 1 {
		t.Fatalf("collectEdges = %d edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].toDir != "b/util" || edges[0].calleeFile != "b/util/util.go" {
		t.Fatalf("edge = %+v, want the imported b/util definition (not lexicographic a/util)", edges[0])
	}
}
