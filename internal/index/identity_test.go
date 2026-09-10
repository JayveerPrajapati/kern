package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	mutTime := time.Now().Add(time.Second)
	_ = os.Chtimes(filepath.Join(dir, "main.go"), mutTime, mutTime)
	if !ix.Stale() {
		t.Error("Stale() must be true when Identity is nil (fail-closed)")
	}
}

// TestTreeOID_CleanTreeFastPath pins the cheap path: on a clean worktree the
// tree OID must equal HEAD^{tree} (the fast path), and untracked tool-state
// dirs (.kern, .blueprint) must NOT dirty the tree — otherwise every index
// save / audit append would flip the OID and defeat freshness.
func TestTreeOID_CleanTreeFastPath(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init")
	testGit(t, dir, "config", "user.name", "t")
	testGit(t, dir, "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "main.go")
	testGit(t, dir, "commit", "-qm", "init")

	head := testGit(t, dir, "rev-parse", "HEAD^{tree}")
	if head == "" {
		t.Fatal("HEAD^{tree} unexpectedly empty")
	}
	if got := treeOID(dir); got != head {
		t.Fatalf("treeOID on clean tree = %q, want HEAD^{tree} %q (fast path)", got, head)
	}

	// Untracked tool-state dirs are excluded from the identity: creating them
	// must not change the OID.
	for _, d := range []string{".kern", ".blueprint"} {
		if err := os.MkdirAll(filepath.Join(dir, d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, d, "sub", "state.jsonl"), []byte("audit entry\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := treeOID(dir); got != head {
		t.Fatalf("treeOID with untracked .kern/.blueprint = %q, want %q (excluded dirs must not dirty the tree)", got, head)
	}
}

// TestTreeOID_DirtyTreeSlowPath: any real change (staged or unstaged, inside
// the compared set) must flip the OID, and the OID must equal the slow path's
// throwaway-index write-tree result (both paths agree on the same file set).
func TestTreeOID_DirtyTreeSlowPath(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init")
	testGit(t, dir, "config", "user.name", "t")
	testGit(t, dir, "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "main.go")
	testGit(t, dir, "commit", "-qm", "init")
	head := testGit(t, dir, "rev-parse", "HEAD^{tree}")

	// Unstaged edit inside the compared set: OID must differ from HEAD and
	// match the slow-path computation (staging the whole tree into a
	// throwaway index produces the same tree object).
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc A() {}\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := treeOID(dir)
	if got == "" || got == head {
		t.Fatalf("treeOID after edit = %q, want non-empty and != HEAD^{tree} %q", got, head)
	}
	// Manual slow-path reproduction: throwaway index staging.
	tmp, err := os.CreateTemp("", "kern-treeoid-check-*")
	if err != nil {
		t.Fatal(err)
	}
	idxPath := tmp.Name()
	tmp.Close()
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(idxPath)
	env := append(os.Environ(), "GIT_INDEX_FILE="+idxPath)
	add := exec.Command("git", "-C", dir, "add", "-A", "--ignore-errors", "--", ".", ":(exclude).kern", ":(exclude).blueprint")
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("throwaway add: %v\n%s", err, out)
	}
	wt := exec.Command("git", "-C", dir, "write-tree")
	wt.Env = env
	out, err := wt.Output()
	if err != nil {
		t.Fatal(err)
	}
	if slow := strings.TrimSpace(string(out)); slow != got {
		t.Fatalf("treeOID = %q, slow-path write-tree = %q (fast/slow must agree)", got, slow)
	}

	// Staging does not change the working tree, so the identity is unchanged:
	// the tree OID measures the tree ON DISK, not the git index. This is what
	// makes the fast path safe — `git add` alone never dirties the identity.
	testGit(t, dir, "add", "main.go")
	if staged := treeOID(dir); staged != got {
		t.Fatalf("treeOID changed after staging = %q, want %q (staging must not change the working-tree identity)", staged, got)
	}
}
