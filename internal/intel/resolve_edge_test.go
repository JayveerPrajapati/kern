package intel

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ambiguousSaveIndex builds an index where the simple name "Save" exists in
// two packages (db and store), so a qualified reference cannot be resolved
// by name alone — only the caller's imports disambiguate it. This is the
// shape of real cross-package edges the AST analyzer records as
// "alias.Func" and is the A3 regression: blast radius used to silently
// drop these (measured 5k+ edges on the kern repo itself).
func ambiguousSaveIndex() *index.Index {
	return &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Save", File: "db/store.go", Line: 1, Lang: "go"},
			{Kind: "func", Name: "Save", File: "store/store.go", Line: 1, Lang: "go"},
			{Kind: "func", Name: "Run", File: "app/run.go", Line: 1, Lang: "go"},
		},
		// Run calls db.Save via an import-qualified reference; the raw
		// callee endpoint "db.Save" matches neither package-scoped node ID.
		Calls: map[string][]index.CallEdge{
			"Run": {index.CallEdge{Target: "db.Save", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"db.Save": {"Run"},
		},
		Pkgs: map[string]*index.Pkg{
			"db":    {Name: "db", Path: "db", Imports: []index.ImportEdge{{Path: "fmt", Confidence: index.ConfidenceHigh}}, Files: []string{"db/store.go"}, Lang: "go"},
			"store": {Name: "store", Path: "store", Imports: []index.ImportEdge{}, Files: []string{"store/store.go"}, Lang: "go"},
			"app":   {Name: "app", Path: "app", Imports: []index.ImportEdge{{Path: "example.com/mod/db", Confidence: index.ConfidenceHigh}, {Path: "example.com/mod/store", Confidence: index.ConfidenceHigh}}, Files: []string{"app/run.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// TestImportQualifiedCalleeLinked is the A3 regression: WhatDoesXDependOn
// must follow the import-qualified cross-package edge "db.Save" from Run to
// db's Save (not store's), instead of silently dropping it because the
// simple name "Save" is ambiguous.
func TestImportQualifiedCalleeLinked(t *testing.T) {
	g := FromIndex(ambiguousSaveIndex())

	deps := g.WhatDoesXDependOn("Run")
	var sawDB, sawStore bool
	for _, n := range deps {
		switch n.ID {
		case "db.Save":
			sawDB = true
		case "store.Save":
			sawStore = true
		}
	}
	if !sawDB {
		t.Errorf("WhatDoesXDependOn(Run) = %v; want db.Save linked via the caller's import", names(deps))
	}
	if sawStore {
		t.Errorf("WhatDoesXDependOn(Run) linked store.Save; the import qualifier db must NOT link to the wrong package: %v", names(deps))
	}

	// The reverse direction: db.Save's dependents must include Run.
	rev := g.WhatDependsOn("db.Save")
	found := false
	for _, n := range rev {
		if n.ID == "app.Run" {
			found = true
		}
	}
	if !found {
		t.Errorf("WhatDependsOn(db.Save) = %v; want app.Run (the cross-package caller)", names(rev))
	}
}

// TestResolveEdgeEndpointForeignStaysUnlinked guards against forging links:
// a qualified reference whose qualifier matches no import of the caller
// (local variable / foreign type receiver) must stay unresolved even when a
// same-named project symbol exists.
func TestResolveEdgeEndpointForeignStaysUnlinked(t *testing.T) {
	g := FromIndex(ambiguousSaveIndex())
	if _, ok := g.ResolveEdgeEndpoint("buf.Save", "Run"); ok {
		t.Error("ResolveEdgeEndpoint(buf.Save) resolved; a qualifier matching no import of the caller must stay unresolved")
	}
	// Uppercase qualifiers (type/variable receivers) never link via imports.
	if _, ok := g.ResolveEdgeEndpoint("Builder.Save", "Run"); ok {
		t.Error("ResolveEdgeEndpoint(Builder.Save) resolved; an uppercase qualifier is a receiver, not a package")
	}
}
