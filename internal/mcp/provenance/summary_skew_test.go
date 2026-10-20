package provenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestSummaryAnnotatesCommitSkew pins when the
// index was built at a different commit than the repo's current HEAD, the
// provenance stamp must say so next to the verdict instead of showing a
// plain "fresh" — the freshness verdict itself stays tree-based and honest.
func TestSummaryAnnotatesCommitSkew(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	headCmd := exec.Command("git", "rev-parse", "--verify", "--short", "HEAD")
	headCmd.Dir = dir
	headOut, err := headCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headOut))

	// Flush any cached HEAD lookup for this root before asserting.
	headCommitMu.Lock()
	delete(headCommitEntry, dir)
	headCommitMu.Unlock()

	ix := &index.Index{Root: dir, UpdatedAt: time.Now()}
	p := &Provenance{Index: IndexProvenance{GitCommit: "0000000", FreshnessVerdict: "fresh"}}
	got := Summary(ix, p)
	if !strings.Contains(got, "commit 0000000") || !strings.Contains(got, "HEAD "+head) || !strings.Contains(got, "indexed content still matches") {
		t.Fatalf("fresh-but-skewed banner must annotate HEAD %s without claiming staleness: %q", head, got)
	}
	if strings.Contains(got, "index behind HEAD") {
		t.Fatalf("fresh verdict must not say the index is behind: %q", got)
	}

	p.Index.FreshnessVerdict = "stale"
	got = Summary(ix, p)
	if !strings.Contains(got, "index behind HEAD") {
		t.Fatalf("stale verdict with skew must say the index is behind HEAD: %q", got)
	}
	p.Index.FreshnessVerdict = "fresh"

	// No skew (index commit == HEAD): the stamp is OMITTED entirely — a
	// fully-fresh git worktree with the recorded commit on HEAD is the
	// normal state, so the one-line footer prints only when the state is
	// unusual (QA 2026-10-19).
	headCommitMu.Lock()
	delete(headCommitEntry, dir)
	headCommitMu.Unlock()
	p.Index.GitCommit = head
	got = Summary(ix, p)
	if got != "" {
		t.Fatalf("in-sync fresh index must omit the footer entirely, got %q", got)
	}
}

// TestHeadCommitCachedNonRepo pins the negative path: a non-git root returns
// "" (cached), so the banner keeps its plain form instead of spawning a git
// process on every response.
func TestHeadCommitCachedNonRepo(t *testing.T) {
	dir := t.TempDir()
	if got := headCommitCached(dir); got != "" {
		t.Fatalf("non-git root: headCommitCached = %q, want empty", got)
	}
	// cached negative
	if got := headCommitCached(dir); got != "" {
		t.Fatalf("non-git root (cached): headCommitCached = %q, want empty", got)
	}
}

// TestSummarySelfHealsContentEqualCommitBump pins the never-self-healing
// defect (QA 2026-10-19): a content-equal commit bump (git commit
// --allow-empty — HEAD advances, the tree is unchanged) made the summary
// print "commit differs; indexed content still matches" forever. The first
// Summary call after the bump must persist the new HEAD as the recorded
// git_commit label (a meta update, never a rebuild), and the next call must
// print nothing — the skew warning self-heals instead of recurring.
func TestSummarySelfHealsContentEqualCommitBump(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")

	// Build directly (not LoadOrBuild): the environment may install kern's
	// global post-commit hook, which can pre-populate .kern with an index
	// whose git_commit label raced the commit (empty). Build always rebuilds
	// and records the commit cleanly.
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ix.Identity == nil || ix.Identity.GitCommit == "" {
		t.Fatalf("built index must record a git commit, got %+v", ix.Identity)
	}

	// Content-equal commit bump: HEAD advances, the tree is unchanged.
	git("commit", "-q", "--allow-empty", "-m", "seal-only bump")

	// Flush the cached HEAD lookup so the bump is observed.
	headCommitMu.Lock()
	delete(headCommitEntry, dir)
	headCommitMu.Unlock()

	// Call 1: observes the skew and self-heals (persists the new label).
	p1 := &Provenance{Index: IndexIdentity(ix, headCommitCached)}
	got1 := Summary(ix, p1)
	if !strings.Contains(got1, "commit differs; indexed content still matches") {
		t.Fatalf("first call after the bump must annotate the skew: %q", got1)
	}
	if ix.Identity.GitCommit == p1.Index.GitCommit {
		t.Fatalf("self-heal did not refresh the recorded label: %q", ix.Identity.GitCommit)
	}

	// Call 2: the refreshed label matches HEAD and the tree is still fresh —
	// the footer is omitted entirely.
	headCommitMu.Lock()
	delete(headCommitEntry, dir)
	headCommitMu.Unlock()
	p2 := &Provenance{Index: IndexIdentity(ix, headCommitCached)}
	got2 := Summary(ix, p2)
	if got2 != "" {
		t.Fatalf("self-healed index must print no footer on the next call, got %q", got2)
	}
}
