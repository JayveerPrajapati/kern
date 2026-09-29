package execution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRedactCredentialsRedactsURLToken is the required H1 test: an output
// line carrying a remote URL with an embedded fake GitHub PAT
// (https://user:ghp_fake@github.com/...) must come out redacted to
// scheme://user:***@host.
func TestRedactCredentialsRedactsURLToken(t *testing.T) {
	token := "ghp_fake12345678901234567890"
	in := "clone https://user:" + token + "@github.com/acme/repo.git and push"
	out := redactCredentials(in)
	if strings.Contains(out, token) {
		t.Errorf("token leaked in output: %s", out)
	}
	if !strings.Contains(out, "https://user:***@github.com/acme/repo.git") {
		t.Errorf("expected redacted URL, got: %s", out)
	}
}

// TestRedactCredentialsRedactsBarePAT guards against leaking a GitHub PAT that
// appears outside a URL (e.g. pasted into a diff content line).
func TestRedactCredentialsRedactsBarePAT(t *testing.T) {
	token := "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
	in := "token " + token + " expired"
	out := redactCredentials(in)
	if strings.Contains(out, token) {
		t.Errorf("bare PAT leaked: %s", out)
	}
}

// TestRedactCredentialsLeavesPlainURLs ensures the scrubber does not mangle
// credential-free URLs (no false positives on normal diff content).
func TestRedactCredentialsLeavesPlainURLs(t *testing.T) {
	in := "https://github.com/acme/repo.git\nhttp://localhost:8080/x\n"
	out := redactCredentials(in)
	if out != in {
		t.Errorf("plain URLs changed:\n%s", out)
	}
}

// TestDiffRedactsCredentialsInOutput exercises the full Diff() pipeline: a
// credentialed URL in changed content (and in a .git/config the aside move
// hides) must never reach the returned diff, and the source repo's .git must
// be restored with no orphaned aside left in the parent.
func TestDiffRedactsCredentialsInOutput(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A .git dir carrying a remote URL with an embedded fake PAT, exercising
	// the aside-move path.
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	token := "ghp_fake12345678901234567890"
	cfg := "[remote \"origin\"]\n\turl = https://user:" + token + "@github.com/acme/repo.git\n"
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	wt, err := NewWorktree(src)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer func() { _ = wt.Cleanup() }()

	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-one\n+https://user:" + token + "@github.com/acme/repo.git\n"
	if err := wt.Apply(patch); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	diff, err := wt.Diff()
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.Contains(diff, token) {
		t.Errorf("PAT leaked in diff:\n%s", diff)
	}
	if !strings.Contains(diff, "https://user:***@github.com/acme/repo.git") {
		t.Errorf("expected redacted URL in diff:\n%s", diff)
	}
	// The source repo's .git must be restored...
	if _, err := os.Stat(filepath.Join(src, ".git", "config")); err != nil {
		t.Errorf(".git not restored after Diff: %v", err)
	}
	// ...and no aside may remain in the parent dir.
	parent := filepath.Dir(src)
	entries, rerr := os.ReadDir(parent)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".kern-git-aside-") {
			t.Errorf("orphaned aside left behind: %s", e.Name())
		}
	}
}

// TestFindOrphanedGitAsides verifies the orphan detector that execution start
// warns about: a .kern-git-aside-* directory stranded in the repo's parent by
// a crashed Diff must be found (and never auto-deleted).
func TestFindOrphanedGitAsides(t *testing.T) {
	src := t.TempDir()
	parent := filepath.Dir(src)
	aside := filepath.Join(parent, ".kern-git-aside-123-456")
	if err := os.MkdirAll(aside, 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(aside) }()

	orphans := findOrphanedGitAsides(src)
	found := false
	for _, o := range orphans {
		if o == aside {
			found = true
		}
	}
	if !found {
		t.Errorf("orphaned aside not detected: %v", orphans)
	}
	// The aside must still exist afterwards (warning-only, no auto-delete).
	if _, err := os.Stat(aside); err != nil {
		t.Errorf("orphan detector deleted the aside: %v", err)
	}
}

// TestRedactCredentialsRedactsBareHexUserinfo guards the oracle-gate advisory
// gap: a classic 40-hex PAT used as the WHOLE userinfo (no colon, no ghp_
// prefix) — https://<token>@host — must be redacted too.
func TestRedactCredentialsRedactsBareHexUserinfo(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef01234567" // 40 hex chars
	in := "fetch https://" + token + "@github.com/acme/repo.git info"
	out := redactCredentials(in)
	if strings.Contains(out, token) {
		t.Errorf("bare-hex userinfo token leaked: %s", out)
	}
	if !strings.Contains(out, "https://***@github.com/acme/repo.git") {
		t.Errorf("expected scheme://***@host form, got: %s", out)
	}
}

// TestFindOrphanedGitAsidesScopedToRepo pins the oracle-gate advisory: only
// asides carrying THIS repo's identity hash (or legacy no-suffix ones) are
// reported — another repo's live aside in the same parent must not trip the
// warning.
func TestFindOrphanedGitAsidesScopedToRepo(t *testing.T) {
	src := t.TempDir()
	parent := filepath.Dir(src)

	ours := filepath.Join(parent, fmt.Sprintf(".kern-git-aside-123-456-%s", repoHash8(src)))
	theirs := filepath.Join(parent, ".kern-git-aside-124-457-deadbeef")
	legacy := filepath.Join(parent, ".kern-git-aside-125-458")
	for _, d := range []string{ours, theirs, legacy} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, d := range []string{ours, theirs, legacy} {
			_ = os.RemoveAll(d)
		}
	}()

	orphans := findOrphanedGitAsides(src)
	has := func(p string) bool {
		for _, o := range orphans {
			if o == p {
				return true
			}
		}
		return false
	}
	if !has(ours) {
		t.Errorf("same-repo aside not reported: %v", orphans)
	}
	if !has(legacy) {
		t.Errorf("legacy aside not reported (must stay conservative): %v", orphans)
	}
	if has(theirs) {
		t.Errorf("different repo's aside reported — must be scoped: %v", orphans)
	}
}
