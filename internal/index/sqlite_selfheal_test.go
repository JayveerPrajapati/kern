//go:build !nosqlite

package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenSQLiteSelfHealsCorruptStore (QA Pick #14, F-IX1): a corrupt
// index.sqlite previously failed every open with a raw driver error and no
// command ever recreated the store. OpenSQLite must quarantine the corrupt
// file (kept for inspection, suffixed .corrupt-<unix>) and succeed on a
// fresh one.
func TestOpenSQLiteSelfHealsCorruptStore(t *testing.T) {
	root := t.TempDir()
	storePath := sqliteDBPath(root)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Garbage that is definitely not a SQLite database.
	if err := os.WriteFile(storePath, []byte("this is definitely not a sqlite database at all, nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSQLite(root)
	if err != nil {
		t.Fatalf("OpenSQLite must self-heal a corrupt store, got %v", err)
	}
	defer s.Close()
	// The fresh store is usable: a Save round-trip works.
	if err := s.Save(&Index{Symbols: []Symbol{{Kind: "func", Name: "Fit", File: "fit.go", Line: 1}}}); err != nil {
		t.Fatalf("Save on the healed store failed: %v", err)
	}
	// The corrupt original was quarantined, not silently destroyed.
	ents, _ := os.ReadDir(filepath.Dir(storePath))
	quarantined := false
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "index.sqlite.corrupt-") {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatalf("corrupt store was not quarantined next to the fresh one")
	}
}
