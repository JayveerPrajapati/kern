package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// call buckets are recorded by bare name, so an
// ambiguous name (New/Run/Save defined in several packages) used to become a
// traversable phantom node pooling every same-named definition's callers —
// "AuthorizeContext" reported 4,767 transitive dependents and "Run" 86, both
// amplified through such hubs. Ambiguous endpoints must be reached-but-
// never-expanded, and an ambiguous bare target must resolve to nothing.
func TestAmbiguousNameDoesNotAmplifyBlastRadius(t *testing.T) {
	ix := &index.Index{Root: "."}
	// Two packages each defining Save and a helper that calls their Save.
	// A callee of ONE Save must not inherit the OTHER Save's callers.
	ix.Symbols = []index.Symbol{
		{Name: "Save", Kind: "func", File: "a/a.go", Lang: "go"},
		{Name: "Save", Kind: "func", File: "b/b.go", Lang: "go"},
		{Name: "helperA", Kind: "func", File: "a/a.go", Lang: "go"},
		{Name: "helperB", Kind: "func", File: "b/b.go", Lang: "go"},
		{Name: "flush", Kind: "func", File: "a/a.go", Lang: "go"},
		{Name: "drain", Kind: "func", File: "b/b.go", Lang: "go"},
	}
	ix.Pkgs = map[string]*index.Pkg{
		"a": {Lang: "go"},
		"b": {Lang: "go"},
	}
	// Bare-name buckets: "Save" merges BOTH definitions' callees.
	ix.Calls = map[string][]index.CallEdge{
		"Save":    {{Target: "flush"}, {Target: "drain"}}, // a.Save→flush, b.Save→drain
		"helperA": {{Target: "Save"}},
		"helperB": {{Target: "Save"}},
	}
	ix.Callers = map[string][]string{
		"Save":  {"helperA", "helperB"},
		"flush": {"Save"},
		"drain": {"Save"},
	}
	g := FromIndex(ix)

	// flush is only called by a.Save. Its dependent set must NOT pull in
	// helperB (b's caller of the OTHER Save) via the merged bucket.
	deps := g.WhatDependsOn("flush")
	names := nodeNames(deps)
	for _, n := range names {
		if n == "helperB" {
			t.Fatalf("helperB (caller of the OTHER Save) leaked into flush's blast radius: %v", names)
		}
	}
	// helperA's call edge to the ambiguous "Save" must still be visible as a
	// reached node... through the opaque marker it is NOT a node; the edge is
	// simply unattributable. The forward query must not expand through the
	// merged bucket either: what a.Save calls cannot be answered per-package.
	calls := g.WhatDoesXDependOn("helperA")
	for _, n := range nodeNames(calls) {
		if n == "flush" || n == "drain" {
			t.Fatalf("ambiguous Save's merged callees attributed to helperA: %v", nodeNames(calls))
		}
	}

	// A bare ambiguous TARGET must resolve to nothing rather than the union
	// of every Save's callers.
	if got := g.WhatDependsOn("Save"); len(got) != 0 {
		t.Fatalf("bare ambiguous target must not return the union of all same-named definitions' callers, got %v", nodeNames(got))
	}
}

// nodeNames renders nodes for assertions, tolerating the file/module nodes
// the graph adds.
func nodeNames(nodes []domain.Node) []string {
	var out []string
	for _, n := range nodes {
		if n.Symbol != nil {
			out = append(out, n.Symbol.Name)
		}
	}
	return out
}
