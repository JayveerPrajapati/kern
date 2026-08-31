package intelligence

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

func sym(kind, name, file string, line int) index.Symbol {
	return index.Symbol{Kind: kind, Name: name, File: file, Line: line, Lang: "go"}
}

// fakeIndex builds a small in-memory v1 index:
// Foo -> Bar, Baz
// Bar -> Baz
// Baz -> Foo            (cycle: Foo->Bar->Baz->Foo and Foo->Baz->Foo)
// HandleUsers -> Foo    (entry point, Framework "net-http", Route "/users")
// TestFoo -> HandleUsers
// svc imports "net/http"
// Baz inherits "extends:Animal"
func fakeIndex() *index.Index {
	ix := &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			sym("func", "Foo", "foo.go", 1),
			sym("func", "Bar", "bar.go", 1),
			sym("func", "Baz", "baz.go", 1),
			{Kind: "func", Name: "HandleUsers", File: "svc/handlers.go", Line: 1, Lang: "go", Entry: true, Framework: "net-http", Route: "/users"},
			sym("func", "TestFoo", "svc/foo_test.go", 1),
		},
		Calls: map[string][]string{
			"Foo":         {"Bar", "Baz"},
			"Bar":         {"Baz"},
			"Baz":         {"Foo"},
			"HandleUsers": {"Foo"},
			"TestFoo":     {"HandleUsers"},
		},
		Callers: map[string][]string{
			"Bar":         {"Foo"},
			"Baz":         {"Foo", "Bar"},
			"Foo":         {"HandleUsers", "Baz"},
			"HandleUsers": {"TestFoo"},
		},
		Inherits: map[string][]string{
			"Baz": {"extends:Animal"},
		},
		InheritedBy: map[string][]string{
			"Animal": {"Baz"},
		},
		Pkgs: map[string]*index.Pkg{
			"svc": {Name: "svc", Path: "svc", Imports: []string{"net/http"}, Files: []string{"svc/handlers.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	return ix
}

// names returns the ID (label) of each node.
func names(nodes []domain.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestFromIndex(t *testing.T) {
	g := FromIndex(fakeIndex())

	// Nodes: 5 symbols + 5 file + 1 module ("svc") = 11.
	if len(g.Nodes) != 11 {
		t.Fatalf("node count = %d, want 11", len(g.Nodes))
	}
	// Edges: 6 calls (Foo->Bar, Foo->Baz, Bar->Baz, Baz->Foo, HandleUsers->Foo,
	// TestFoo->HandleUsers) + 1 inherits + 1 imports + 5 contains + 5 defines = 18.
	if len(g.Edges) != 18 {
		t.Fatalf("edge count = %d, want 18", len(g.Edges))
	}

	// Provenance is set for AST extraction.
	if g.Provenance == nil {
		t.Fatal("Provenance not set")
	}
	if g.Provenance.Source != "ast" || g.Provenance.Confidence != 1.0 {
		t.Errorf("provenance = %+v, want ast/1.0", g.Provenance)
	}
	if g.Provenance.ExtractedAt.IsZero() {
		t.Error("provenance ExtractedAt is zero")
	}

	// Version metadata is set and stable.
	if g.Version == nil {
		t.Fatal("Version not set")
	}
	if g.Version.SymbolCount != 5 || g.Version.EdgeCount != 18 {
		t.Errorf("version = symbols %d edges %d, want 5/18", g.Version.SymbolCount, g.Version.EdgeCount)
	}
	if g.Version.GraphHash == "" {
		t.Error("graph hash is empty")
	}
	if g.Version.CommitHash != "" {
		t.Errorf("commit hash should be empty, got %q", g.Version.CommitHash)
	}
}

func TestGraphHashStable(t *testing.T) {
	a := FromIndex(fakeIndex())
	b := FromIndex(fakeIndex())
	if a.Version.GraphHash != b.Version.GraphHash {
		t.Fatalf("graph hash not stable: %q != %q", a.Version.GraphHash, b.Version.GraphHash)
	}
	if a.Version.GraphHash == "" {
		t.Fatal("graph hash is empty")
	}
}

func TestWhoCalls(t *testing.T) {
	g := FromIndex(fakeIndex())
	got := g.WhoCalls("Foo")
	ids := names(got)
	// Direct callers of Foo: HandleUsers and Baz.
	if len(ids) != 2 || !contains(ids, "svc.HandleUsers") || !contains(ids, "Baz") {
		t.Fatalf("WhoCalls(Foo) = %v, want [svc.HandleUsers Baz]", ids)
	}
	// WhoCalls is direct: Bar does not call Foo directly.
	if contains(ids, "Bar") {
		t.Errorf("WhoCalls(Foo) includes transitive caller Bar: %v", ids)
	}
}

func TestWhatDependsOnIsTransitive(t *testing.T) {
	g := FromIndex(fakeIndex())
	ids := names(g.WhatDependsOn("Foo"))
	// Transitive callers of Foo: Bar, Baz, HandleUsers, TestFoo.
	if len(ids) != 4 {
		t.Fatalf("WhatDependsOn(Foo) = %v, want 4 distinct transitive callers", ids)
	}
	for _, want := range []string{"Bar", "Baz", "svc.HandleUsers", "svc.TestFoo"} {
		if !contains(ids, want) {
			t.Errorf("WhatDependsOn(Foo) missing %q: %v", want, ids)
		}
	}
}

func TestWhatDoesXDependOnIsTransitive(t *testing.T) {
	g := FromIndex(fakeIndex())
	ids := names(g.WhatDoesXDependOn("Foo"))
	// Transitive callees of Foo: Bar and Baz (Foo's own edge target set).
	if len(ids) != 2 || !contains(ids, "Bar") || !contains(ids, "Baz") {
		t.Fatalf("WhatDoesXDependOn(Foo) = %v, want [Bar Baz]", ids)
	}
}

func TestCycleSafety(t *testing.T) {
	ix := &index.Index{
		Root:      "/cycle",
		Symbols:   []index.Symbol{sym("func", "A", "a.go", 1), sym("func", "B", "b.go", 1)},
		Calls:     map[string][]string{"A": {"B"}, "B": {"A"}},
		Callers:   map[string][]string{"A": {"B"}, "B": {"A"}},
		UpdatedAt: time.Now(),
	}
	g := FromIndex(ix)
	// Both forward and reverse traversals must terminate on a 2-cycle.
	if ids := names(g.WhatDoesXDependOn("A")); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("WhatDoesXDependOn(A) in cycle = %v, want [B]", ids)
	}
	if ids := names(g.WhatDependsOn("A")); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("WhatDependsOn(A) in cycle = %v, want [B]", ids)
	}
	if ids := names(g.WhoCalls("A")); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("WhoCalls(A) in cycle = %v, want [B]", ids)
	}
}

func TestWhatAPIsAffected(t *testing.T) {
	g := FromIndex(fakeIndex())
	ids := names(g.WhatAPIsAffected("Foo"))
	if len(ids) != 1 || ids[0] != "svc.HandleUsers" {
		t.Fatalf("WhatAPIsAffected(Foo) = %v, want [svc.HandleUsers]", ids)
	}
	// A change to the entry point itself affects that entry point.
	if ids := names(g.WhatAPIsAffected("HandleUsers")); len(ids) != 1 || ids[0] != "svc.HandleUsers" {
		t.Fatalf("WhatAPIsAffected(HandleUsers) = %v, want [svc.HandleUsers]", ids)
	}
}

func TestWhatServicesAffected(t *testing.T) {
	g := FromIndex(fakeIndex())
	ids := names(g.WhatServicesAffected("Foo"))
	// Affected API entry (HandleUsers) lives in package "svc", which is a module node.
	if len(ids) != 1 || ids[0] != "svc" {
		t.Fatalf("WhatServicesAffected(Foo) = %v, want [svc]", ids)
	}
}

func TestWhatEventsAffectedIsEmpty(t *testing.T) {
	g := FromIndex(fakeIndex())
	if got := g.WhatEventsAffected("Foo"); len(got) != 0 {
		t.Fatalf("WhatEventsAffected(Foo) = %v, want empty (Phase 11 placeholder)", names(got))
	}
}

func TestWhatTestsCover(t *testing.T) {
	g := FromIndex(fakeIndex())
	ids := names(g.WhatTestsCover("Foo"))
	// TestFoo transitively reaches Foo via HandleUsers.
	if len(ids) != 1 || ids[0] != "svc.TestFoo" {
		t.Fatalf("WhatTestsCover(Foo) = %v, want [svc.TestFoo]", ids)
	}
}

func TestWhatTestsCoverEmptyWhenNone(t *testing.T) {
	g := FromIndex(fakeIndex())
	// A symbol no test reaches has no covering tests.
	if got := g.WhatTestsCover("DoesNotExist"); len(got) != 0 {
		t.Fatalf("WhatTestsCover(DoesNotExist) = %v, want empty", names(got))
	}
}

// fakeIndex2 returns a graph with an entry point that is itself a handler and
// a test that calls it, used to keep entry/service assertions isolated.
func fakeIndex2() *index.Index {
	return &index.Index{
		Root:    "/fake2",
		Symbols: []index.Symbol{sym("func", "Compute", "svc/calc.go", 1)},
		Calls:   map[string][]string{"Compute": {}},
		Callers: map[string][]string{},
		Pkgs:    map[string]*index.Pkg{},
	}
}

func TestWhoCallsUnknownSymbol(t *testing.T) {
	g := FromIndex(fakeIndex2())
	if got := g.WhoCalls("DoesNotExist"); len(got) != 0 {
		t.Fatalf("WhoCalls(missing) = %v, want empty", names(got))
	}
}

// TestSameNamedSymbolsDoNotCollide guards Bug #6: two same-named package-level
// symbols in different packages must get distinct, package-scoped node IDs so
// their caller sets are not conflated.
func TestSameNamedSymbolsDoNotCollide(t *testing.T) {
	ix := &index.Index{
		Root: "/collide",
		Symbols: []index.Symbol{
			sym("func", "Save", "db/save.go", 1),
			sym("func", "Save", "api/save.go", 1),
		},
		Calls: map[string][]string{
			"Save": {"db.Save"}, // db.Save calls api.Save
		},
		Pkgs: map[string]*index.Pkg{
			"db":  {Name: "db", Path: "db", Files: []string{"db/save.go"}},
			"api": {Name: "api", Path: "api", Files: []string{"api/save.go"}},
		},
	}
	g := FromIndex(ix)

	// Both Save symbols become distinct nodes (not a single "Save").
	byID := g.nodesByID()
	if _, ok := byID["db.Save"]; !ok {
		t.Fatalf("missing node %q", "db.Save")
	}
	if _, ok := byID["api.Save"]; !ok {
		t.Fatalf("missing node %q", "api.Save")
	}
	// A node whose ID is exactly the bare name must not exist for either.
	if _, ok := byID["Save"]; ok {
		t.Errorf("unexpected bare node %q; node IDs must be package-scoped", "Save")
	}
}
