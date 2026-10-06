package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTreeOIDUnreadableFileFailsClosed pins B3 (deep-dive 2026-10-03): the
// tree-OID slow path must NOT use --ignore-errors. That flag silently drops
// unreadable/locked files from the throwaway index, so an edit to such a file
// would be invisible to the tree OID and a stale index could be judged fresh.
// With the flag removed, an unreadable file makes `git add` fail, treeOID
// returns "" (inconclusive), and callers fall back to the content-hash proof
// — fail-closed.
func TestTreeOIDUnreadableFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "init")
	testGit(t, dir, "config", "user.email", "t@example.com")
	testGit(t, dir, "config", "user.name", "t")
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-m", "init")

	// A tracked file with a change, made unreadable: `git add` cannot stage
	// it, so the slow path must refuse to produce an OID instead of silently
	// computing one without the file's new content.
	locked := filepath.Join(dir, "locked.go")
	if err := os.WriteFile(locked, []byte("package x\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(locked, 0o644) }() // temp dir cleanup

	if oid := treeOID(dir); oid != "" {
		t.Fatalf("treeOID must fail closed (\"\") when a worktree file is unreadable; got an OID computed without it: %q", oid)
	}
}
