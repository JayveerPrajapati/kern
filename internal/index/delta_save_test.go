//go:build !nosqlite

package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeltaSaveEquivalentToFullSave(t *testing.T) {
	root := t.TempDir()
	files := writeDeltaFixture(t, root)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}

	prev, err := Load(root)
	if err != nil || prev == nil {
		t.Fatalf("Load after build: %v", err)
	}
	files["lib/b.go"] = `package lib

func helper() int { return 2 }

func Caller() int { return helper() + 1 }

const AddedConst = 7
`
	files["lib/extra.go"] = `package lib

func Extra() int { return AddedConst }
`
	writeDeltaFiles(t, root, files)
	if err := os.Remove(filepath.Join(root, "app/main.go")); err != nil {
		t.Fatal(err)
	}

	next, err := Update(root, prev)
	if err != nil {
		t.Fatal(err)
	}
	if next.delta == nil || len(next.delta.changed) == 0 || len(next.delta.removed) == 0 {
		t.Fatalf("Update did not record the change set: %+v", next.delta)
	}
	if err := next.Save(); err != nil {
		t.Fatalf("delta Save: %v", err)
	}
	deltaIx, err := Load(root)
	if err != nil || deltaIx == nil {
		t.Fatalf("Load after delta save: %v", err)
	}

	// Same in-memory index, full write, reload: the two stores must agree.
	next.delta = nil
	if err := next.Save(); err != nil {
		t.Fatalf("full Save: %v", err)
	}
	fullIx, err := Load(root)
	if err != nil || fullIx == nil {
		t.Fatalf("Load after full save: %v", err)
	}

	if got, want := len(deltaIx.Symbols), len(fullIx.Symbols); got != want {
		t.Fatalf("symbol count delta=%d full=%d", got, want)
	}
	if got, want := len(deltaIx.Calls), len(fullIx.Calls); got != want {
		t.Fatalf("call owner count delta=%d full=%d", got, want)
	}
	if got, want := len(deltaIx.FileHashes), len(fullIx.FileHashes); got != want {
		t.Fatalf("file count delta=%d full=%d", got, want)
	}
	if got, want := deltaIx.FileHashes["lib/b.go"], fullIx.FileHashes["lib/b.go"]; got != want {
		t.Fatalf("edited file hash delta=%q full=%q", got, want)
	}
	if _, ok := deltaIx.FileHashes["app/main.go"]; ok {
		t.Fatal("removed file still present after delta save")
	}
	dNames, fNames := symbolNames(deltaIx), symbolNames(fullIx)
	if len(dNames) != len(fNames) {
		t.Fatalf("symbol lists differ: %v vs %v", dNames, fNames)
	}
	for i := range dNames {
		if dNames[i] != fNames[i] {
			t.Fatalf("symbol list diverged at %d: %q vs %q", i, dNames[i], fNames[i])
		}
	}
	deltaDef, ok1 := deltaIx.ResolveName("Caller")
	fullDef, ok2 := fullIx.ResolveName("Caller")
	if !ok1 || !ok2 || deltaDef.File != fullDef.File {
		t.Fatalf("resolution diverged: %v/%v vs %v/%v", deltaDef.File, ok1, fullDef.File, ok2)
	}
	store, err := OpenSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	hits, err := store.SearchFTS("Extra", 5)
	if err != nil || len(hits) == 0 || hits[0].Name != "Extra" {
		t.Fatalf("FTS after delta save: hits=%v err=%v", hits, err)
	}
	if got, want := len(deltaIx.CommunityLabels()), len(fullIx.CommunityLabels()); got != want {
		t.Fatalf("community labels delta=%d full=%d", got, want)
	}
}

// TestUpdateNoOpSkipsPersist pins the no-op fast path: touching a file without
// changing its content must not rewrite the store.
func TestUpdateNoOpSkipsPersist(t *testing.T) {
	root := t.TempDir()
	files := writeDeltaFixture(t, root)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	prev, err := Load(root)
	if err != nil || prev == nil {
		t.Fatalf("Load: %v", err)
	}
	// Rewrite the same bytes: mtime changes, content hash does not.
	writeDeltaFiles(t, root, files)
	next, err := Update(root, prev)
	if err != nil {
		t.Fatal(err)
	}
	if !next.skipPersist {
		t.Fatal("content-identical update did not take the no-op fast path")
	}
	if err := next.Save(); err != nil {
		t.Fatalf("no-op Save: %v", err)
	}
	again, err := Load(root)
	if err != nil || again == nil {
		t.Fatalf("Load after no-op save: %v", err)
	}
	if got, want := len(again.Symbols), len(prev.Symbols); got != want {
		t.Fatalf("no-op save changed the store: %d symbols, want %d", got, want)
	}
}
