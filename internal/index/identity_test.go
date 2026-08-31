package index

import (
	"os"
	"path/filepath"
	"testing"
)

// testGit runs a git command in dir via the package's runGit helper, failing
// the test on error.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// TestFreshnessProof_GitApplyDetected is the P0.2 regression test: an edit
// that preserves the file's mtime (exactly what `git apply` does) must still
// flip the freshness verdict to stale, even though the old mtime fast gate
// would have served the index as fresh.
func TestFreshnessProof_GitApplyDetected(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init")
	mainGo := "package main\n\nfunc A() {}\n\nfunc B() {}\n"
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "main.go")

	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Identity == nil {
		t.Fatal("Build must set Identity")
	}
	if ix.Identity.TreeOID == "" {
		t.Fatal("Identity.TreeOID must be set for a git worktree")
	}

	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("initial FreshnessProof verdict = %q; want %q (recorded tree %s)",
			p.Verdict, FreshnessFresh, p.Recorded.TreeOID)
	}
	if ix.Stale() {
		t.Fatal("Stale() should be false right after Build")
	}

	// Capture the pre-edit mtime, mutate the file, then restore the mtime to
	// simulate git apply's mtime-preserving edit.
	fi, err := os.Stat(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	oldMtime := fi.ModTime()

	mutated := mainGo + "func C() {}\n"
	if err := os.WriteFile(mainPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(mainPath, oldMtime, oldMtime); err != nil {
		t.Fatal(err)
	}
	fi2, err := os.Stat(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fi2.ModTime().Equal(oldMtime) {
		t.Fatalf("test setup: mtime not preserved (%v != %v)", fi2.ModTime(), oldMtime)
	}

	p := ix.FreshnessProof(dir)
	if p.Verdict != FreshnessStale {
		t.Errorf("after mtime-preserving edit: verdict = %q; want %q (tree %s -> %s)",
			p.Verdict, FreshnessStale, p.Recorded.TreeOID, p.Current.TreeOID)
	}
	if !ix.Stale() {
		t.Error("Stale() must be true after an mtime-preserving edit (git apply regression)")
	}
}

// TestFreshnessProof_NonGitRepo: without git, TreeOID is empty and the
// content root is the only signal — any edit must flip the verdict to stale.
func TestFreshnessProof_NonGitRepo(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc A() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Identity == nil {
		t.Fatal("Build must set Identity")
	}
	if ix.Identity.TreeOID != "" {
		t.Fatalf("non-git repo must have empty TreeOID, got %q", ix.Identity.TreeOID)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("initial FreshnessProof verdict = %q; want %q", p.Verdict, FreshnessFresh)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc A() {}\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := ix.FreshnessProof(dir)
	if p.Verdict != FreshnessStale {
		t.Errorf("after edit in non-git repo: verdict = %q; want %q", p.Verdict, FreshnessStale)
	}
	if p.Current.TreeOID != "" {
		t.Errorf("current TreeOID should stay empty in a non-git repo, got %q", p.Current.TreeOID)
	}
}

// TestFreshnessProof_NilIdentity: an index without a recorded identity cannot
// be proven — the verdict is "unknown", and Stale() fails closed to true.
func TestFreshnessProof_NilIdentity(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc A() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix.Identity = nil

	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessUnknown {
		t.Errorf("FreshnessProof verdict = %q; want %q", p.Verdict, FreshnessUnknown)
	}
	if p := ix.FreshnessProofStrict(dir); p.Verdict != FreshnessUnknown {
		t.Errorf("FreshnessProofStrict verdict = %q; want %q", p.Verdict, FreshnessUnknown)
	}
	// Mutate so even the defensive legacyStale hash walk reports a change;
	// Stale must return true (fail-closed) regardless.
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc A() {}\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ix.Stale() {
		t.Error("Stale() must be true when Identity is nil (fail-closed)")
	}
}
