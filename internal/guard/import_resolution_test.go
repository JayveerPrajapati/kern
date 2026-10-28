package guard

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestCheckBoundariesFollowsImportForSameBasename pins the guard half of the
// resolver fix: two packages named "util" define the same function, the
// caller imports b/util, and the forbidden edge must be app -> b/util (not the
// lexicographically smaller a/util).
func TestCheckBoundariesFollowsImportForSameBasename(t *testing.T) {
	aSym := index.Symbol{Name: "Helper", Kind: "func", File: "a/util/util.go", Line: 1}
	bSym := index.Symbol{Name: "Helper", Kind: "func", File: "b/util/util.go", Line: 2}
	caller := index.Symbol{Name: "Caller", Kind: "func", File: "app/main.go", Line: 1}
	ix := &index.Index{
		Root: "/repo",
		Symbols: []index.Symbol{
			aSym, bSym, caller,
		},
		SymbolsByFile: map[string][]index.Symbol{
			"a/util/util.go": {aSym},
			"b/util/util.go": {bSym},
			"app/main.go":    {caller},
		},
		Calls:   map[string][]index.CallEdge{"Caller": {{Target: "util.Helper"}}},
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
	b := &Boundaries{Rules: []domain.BoundaryRule{{
		From: "app", To: "b/util", Action: "forbid",
	}}}
	v, _ := CheckBoundariesPrecise(ix, b, []string{"app/main.go"}, false)
	if len(v) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(v), v)
	}
	if v[0].CalleeFile != "b/util/util.go" || v[0].Symbol != "Helper" {
		t.Fatalf("violation = %+v, want the imported b/util Helper", v[0])
	}
}
