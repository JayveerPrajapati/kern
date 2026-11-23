package intel

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

func TestClosureMemoBudget(t *testing.T) {
	old := closureMemoBudget
	closureMemoBudget = 3
	defer func() { closureMemoBudget = old }()

	g := &Graph{}
	for i := 0; i < 5; i++ {
		got := g.rememberClosureLocked(closureKey{start: strconv.Itoa(i)}, []string{"node"})
		if len(got) != 1 {
			t.Fatalf("closure %d was not returned verbatim: %v", i, got)
		}
	}
	if len(g.closureMemo) > 3 {
		t.Fatalf("closure memo retained %d entries, want <= 3", len(g.closureMemo))
	}
	if g.closureMemoIDs > 3 {
		t.Fatalf("closure memo retained %d IDs, want <= 3", g.closureMemoIDs)
	}
	if _, ok := g.closureMemo[closureKey{start: "0"}]; ok {
		t.Fatal("oldest closure was not evicted from the bounded memo")
	}
	if _, ok := g.closureMemo[closureKey{start: "4"}]; !ok {
		t.Fatal("newest closure was evicted from the bounded memo")
	}
}

func TestRememberClosureLockedReusesExistingKey(t *testing.T) {
	g := &Graph{}
	key := closureKey{start: "hub"}
	g.rememberClosureLocked(key, []string{"first"})

	second := g.rememberClosureLocked(key, []string{"second"})
	if len(second) != 1 || second[0] != "first" {
		t.Fatalf("existing closure was replaced: %v", second)
	}
}

func TestRememberClosureLockedCompactsEvictedOrder(t *testing.T) {
	old := closureMemoBudget
	closureMemoBudget = 0
	defer func() { closureMemoBudget = old }()

	g := &Graph{}
	g.rememberClosureLocked(closureKey{start: "hub"}, []string{"node"})

	if g.closureOrder != nil || g.closureHead != 0 || len(g.closureMemo) != 0 {
		t.Fatalf("fully-evicted closure order was not compacted: order=%v head=%d memo=%v", g.closureOrder, g.closureHead, g.closureMemo)
	}
}
