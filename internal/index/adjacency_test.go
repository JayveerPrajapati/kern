package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAdjacencyMatchesCatalog pins the persisted-caller contract: for every
// symbol the adjacency served by CallersFor equals the binding-aware catalog
// result, before and after a delta-written update.
func TestAdjacencyMatchesCatalog(t *testing.T) {
	root := t.TempDir()
	files := writeDeltaFixture(t, root)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Adjacency == nil {
		t.Fatal("Save did not persist the caller adjacency")
	}
	assertAdjacencyMatches(t, loaded, "after build")

	files["lib/b.go"] = `package lib

func helper() int { return 3 }

func Caller() int { return helper() + 1 }
`
	writeDeltaFiles(t, root, files)
	next, err := Update(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(root)
	if err != nil || reloaded == nil {
		t.Fatalf("Load after update: %v", err)
	}
	if reloaded.Adjacency == nil {
		t.Fatal("delta Save did not persist the caller adjacency")
	}
	assertAdjacencyMatches(t, reloaded, "after update")
}

// TestAdjacencyEmptyForCallerlessSymbols pins that the map carries only
// symbols with callers, so a callerless symbol still resolves via the catalog.
func TestAdjacencyEmptyForCallerlessSymbols(t *testing.T) {
	root := t.TempDir()
	writeDeltaFiles(t, root, map[string]string{"main.go": "package main\n\nfunc lonely() {}\n\nfunc main() {}\n"})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	adj := ix.buildCallerAdjacency()
	for _, s := range ix.Symbols {
		if s.Name == "lonely" {
			if _, ok := adj[s.DeclarationID()]; ok {
				t.Fatalf("callerless symbol %q persisted in the adjacency", s.Name)
			}
		}
	}
}

func assertAdjacencyMatches(t *testing.T, ix *Index, when string) {
	t.Helper()
	if len(ix.Adjacency) == 0 {
		t.Fatalf("%s: adjacency is empty", when)
	}
	for _, s := range ix.Symbols {
		want := ix.callersViaCatalog(s)
		got := ix.CallersFor(s)
		if len(want) != len(got) {
			t.Fatalf("%s: %s callers differ: catalog=%v adjacency=%v", when, s.FullName(), want, got)
		}
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("%s: %s callers differ: catalog=%v adjacency=%v", when, s.FullName(), want, got)
			}
		}
	}
}

// TestAdjacencyInvalidatedByInPlaceMutation documents the invariant: in-place
// call-edge mutation must clear the cache (Update/Build produce a fresh index;
// only direct field mutation needs this).
func TestAdjacencyInvalidatedByInPlaceMutation(t *testing.T) {
	root := t.TempDir()
	writeDeltaFixture(t, root)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded == nil {
		t.Fatal(err)
	}
	loaded.Adjacency = nil
	var helper Symbol
	for _, s := range loaded.Symbols {
		if s.Name == "helper" {
			helper = s
			break
		}
	}
	if helper.Name == "" {
		t.Fatal("fixture has no helper symbol")
	}
	if got := loaded.CallersFor(helper); len(got) == 0 {
		t.Fatalf("catalog fallback returned no callers after clearing the cache")
	}
	if _, err := os.Stat(filepath.Join(root, "lib/b.go")); err != nil {
		t.Fatal(err)
	}
}
