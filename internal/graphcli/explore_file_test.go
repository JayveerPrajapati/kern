package graphcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestIndexedFileConfinedToRoot pins the 2026-10-28 campaign fixes: explore
// file-mode must not report success for paths outside the project root, and
// a missing in-root path is a failed lookup (the caller falls through to
// symbol lookup and exits 1) — never a misleading "file: … (0 symbols)".
func TestIndexedFileConfinedToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.go"), []byte("package p\n\nfunc Ok() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// An existing in-root file resolves.
	if rel, ok := indexedFile(ix, root, "real.go"); !ok || rel != "real.go" {
		t.Fatalf("in-root existing file should resolve, got (%q, %v)", rel, ok)
	}
	// A missing in-root file is a failed lookup.
	if _, ok := indexedFile(ix, root, "nonexistent.go"); ok {
		t.Fatal("missing file must not resolve")
	}
	// Absolute out-of-root paths (/dev/null exists on disk) are confined.
	if _, ok := indexedFile(ix, root, "/dev/null"); ok {
		t.Fatal("out-of-root absolute path must not resolve")
	}
	// Relative escapes are confined.
	if _, ok := indexedFile(ix, root, filepath.Join("..", "escape.go")); ok {
		t.Fatal("out-of-root relative path must not resolve")
	}
}
