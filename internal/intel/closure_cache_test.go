package intel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestClosureCachedPerGraph guards the fix that memoizes full transitive
// closures per (graph, start, direction, precision mode): a second identical
// query must return the same cached slice (identical backing array) rather
// than re-walking the reachable subgraph. kern impact recomputes the reverse
// closure up to three times (WhatAPIsAffected, WhatServicesAffected,
// ProductionCriticality) and the forward closure twice (the
// WhatDoesXDependOn variants); before the memo each query re-walked the
// graph.
func TestClosureCachedPerGraph(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Hub() {}\n\nfunc A() { Hub() }\n\nfunc B() { Hub() }\n\nfunc C() { A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := FromIndex(ix)
	start := g.resolveSymbol("Hub")

	rev1 := g.closure(start, false, true)
	rev2 := g.closure(start, false, true)
	if len(rev1) == 0 {
		t.Fatal("fixture produced no reverse closure")
	}
	if reflect.ValueOf(rev1).Pointer() != reflect.ValueOf(rev2).Pointer() {
		t.Fatal("reverse closure recomputed on second call - memo not effective")
	}

	fwd1 := g.closure(start, false, false)
	fwd2 := g.closure(start, false, false)
	if reflect.ValueOf(fwd1).Pointer() != reflect.ValueOf(fwd2).Pointer() {
		t.Fatal("forward closure recomputed on second call - memo not effective")
	}

	s1 := g.closure(start, true, true)
	s2 := g.closure(start, true, true)
	if reflect.ValueOf(s1).Pointer() != reflect.ValueOf(s2).Pointer() {
		t.Fatal("strict closure recomputed on second call - memo not effective")
	}
}
