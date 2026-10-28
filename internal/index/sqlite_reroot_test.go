//go:build !nosqlite

package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSQLiteLoadReRootsCopiedStore pins the 2026-10-28 campaign fix: when a
// whole tree (including .kern/index.sqlite) is copied to a new path, the
// persisted meta root still names the ORIGINAL absolute path while the
// content itself is root-relative. The SQLite loader must serve the label
// of the load root — the JSON loader's reRootIndex behavior — never the
// stale one.
func TestSQLiteLoadReRootsCopiedStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orig := writeTree(t, map[string]string{"main.go": srcMain})
	if _, err := Build(orig); err != nil {
		t.Fatal(err)
	}
	built, err := Build(orig)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(orig, built); err != nil {
		t.Fatal(err)
	}

	// Copy the tree INCLUDING .kern to a new path (a repo copy).
	copy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(copy, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(orig, ".kern", "index.sqlite"))
	if err != nil {
		t.Fatalf("read source store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(copy, ".kern", "index.sqlite"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSQLite(copy)
	if err != nil {
		t.Fatalf("LoadSQLite on copied store: %v", err)
	}
	absCopy, err := filepath.Abs(copy)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got.Root) != filepath.Clean(absCopy) {
		t.Fatalf("loaded Root = %q; want the COPY root %q (stale label must be re-rooted)", got.Root, absCopy)
	}
	if filepath.Clean(got.Root) == filepath.Clean(orig) {
		t.Fatalf("loaded Root still points at the ORIGINAL tree %q", orig)
	}
	// The content itself is root-relative and must survive the copy.
	if len(got.Symbols) == 0 {
		t.Fatal("copied store lost its symbols")
	}
}
