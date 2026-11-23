//go:build !nosqlite

package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSQLiteAliasRoundTrip pins persistence of the alias field: the type
// surfaces read it from the loaded index, so a store that drops it silently
// reintroduces the alias all-clear.
func TestSQLiteAliasRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	ix := &Index{
		Root:        dir,
		Version:     indexVersion,
		Symbols:     []Symbol{{Kind: "type", Name: "A", File: "a.go", Line: 3, Alias: "B", Lang: "go"}},
		FileHashes:  map[string]string{"a.go": "h"},
		Calls:       map[string][]CallEdge{},
		Callers:     map[string][]string{},
		Inherits:    map[string][]string{},
		Communities: map[string]string{},
		Pkgs:        map[string]*Pkg{},
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatalf("SaveSQLite: %v", err)
	}
	store, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer func() { _ = store.Close() }()
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, s := range got.Symbols {
		if s.Name == "A" {
			if s.Alias != "B" {
				t.Fatalf("alias lost in the SQLite round trip: got %q, want %q", s.Alias, "B")
			}
			return
		}
	}
	t.Fatalf("symbol A missing after load: %+v", got.Symbols)
}
