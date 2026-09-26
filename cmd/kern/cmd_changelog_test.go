package main

import (
	"os/exec"
	"strings"
	"testing"
)

// commitmsgGit runs `git -C dir <args...>` failing the test on error.
func commitmsgGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newCommitmsgChangelogRepo initializes a git repo with conventional commits
// for the CLI changelog tests.
func newCommitmsgChangelogRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	commitmsgGit(t, dir, "init", "-q")
	commitmsgGit(t, dir, "config", "user.name", "cl")
	commitmsgGit(t, dir, "config", "user.email", "cl@t")
	commitmsgGit(t, dir, "commit", "--allow-empty", "-qm", "chore(repo): init")
	for _, m := range []string{"feat(api): add endpoint", "fix(api): retry on 429", "docs(api): document"} {
		commitmsgGit(t, dir, "commit", "--allow-empty", "-qm", m)
	}
	return dir
}

// TestCommitmsgChangelogRendersDraft pins the CLI path: `kern commitmsg
// --changelog <range>` prints the subsystem-grouped draft to stdout.
func TestCommitmsgChangelogRendersDraft(t *testing.T) {
	root := newCommitmsgChangelogRepo(t)
	stdout := captureStdout(t, func() {
		runCommitmsg([]string{"--changelog", "HEAD~3..HEAD", "--root", root})
	})
	if !strings.Contains(stdout, "Changelog: HEAD~3..HEAD — 3 commits") {
		t.Errorf("missing header:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## api") {
		t.Errorf("missing subsystem section:\n%s", stdout)
	}
	if !strings.Contains(stdout, "### feat") || !strings.Contains(stdout, "### fix") || !strings.Contains(stdout, "### docs") {
		t.Errorf("missing type sections:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- add endpoint (") {
		t.Errorf("missing bullet:\n%s", stdout)
	}
}

// TestCommitmsgChangelogWinsOverMessagePath pins the mutual-exclusion
// contract: when --changelog and message-path flags are both given, the
// changelog wins and a note is printed on stderr.
func TestCommitmsgChangelogWinsOverMessagePath(t *testing.T) {
	root := newCommitmsgChangelogRepo(t)
	var stdout, stderr string
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runCommitmsg([]string{"--changelog", "HEAD~1..HEAD", "--root", root, "--staged", "--subject"})
		})
	})
	if !strings.Contains(stdout, "Changelog: HEAD~1..HEAD — 1 commits") {
		t.Errorf("changelog did not win:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--changelog given") {
		t.Errorf("missing mutual-exclusion note on stderr: %q", stderr)
	}
}

// TestCommitmsgChangelogEmptyRangeExits1 pins the exit code: an empty range is
// a clear error and exits 1 (not the usage-error 2, and not the message path).
func TestCommitmsgChangelogEmptyRangeExits1(t *testing.T) {
	root := newCommitmsgChangelogRepo(t)
	stderr, code := captureStderrExit(t, func() {
		runCommitmsg([]string{"--changelog", "", "--root", root})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "empty revision range") {
		t.Errorf("stderr missing clear error: %q", stderr)
	}
}

// TestCommitmsgChangelogNonGitRootExits1 pins the non-git error path: a root
// that is not a repository fails with exit 1 and a clear message.
func TestCommitmsgChangelogNonGitRootExits1(t *testing.T) {
	plain := t.TempDir()
	stderr, code := captureStderrExit(t, func() {
		runCommitmsg([]string{"--changelog", "HEAD~1..HEAD", "--root", plain})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "changelog:") {
		t.Errorf("stderr missing changelog error: %q", stderr)
	}
}
