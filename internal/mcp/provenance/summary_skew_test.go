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

	// No skew (index commit == HEAD): plain form, no annotation.
	headCommitMu.Lock()
	delete(headCommitEntry, dir)
	headCommitMu.Unlock()
	p.Index.GitCommit = head
	got = Summary(ix, p)
	if strings.Contains(got, "behind") {
		t.Fatalf("in-sync banner must not claim skew: %q", got)
	}
	if !strings.Contains(got, "commit "+head) {
		t.Fatalf("banner must still show the commit: %q", got)
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
