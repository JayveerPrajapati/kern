package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIndexSkipsUnreadableDir pins the 2026-10-28 campaign finding: one
// unreadable subdirectory (permission denied mid-tree) must not abort the
// whole index — ripgrep-style warn-and-skip. The readable siblings still
// index and search; only the locked subtree is missing. The root itself
// failing still errors (that contract lives in the walk's root guard).
func TestIndexSkipsUnreadableDir(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("readable.go", "package p\n\nfunc Ok() {}\n")
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join("locked", "secret.go"), "package locked\n\nfunc Hidden() {}\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	defer func() { _ = os.Chmod(locked, 0o755) }() // temp dir cleanup

	ix, err := Build(root)
	if err != nil {
		t.Fatalf("Build aborted on an unreadable subdirectory (F-2 regression): %v", err)
	}
	var okFound bool
	for _, s := range ix.Symbols {
		if s.Name == "Hidden" {
			t.Fatal("symbol inside the unreadable directory must not be indexed")
		}
		if s.Name == "Ok" {
			okFound = true
		}
	}
	if !okFound {
		t.Fatal("readable sibling file must still be indexed (F-2: skip, not abort)")
	}
}
