package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	base := []string{"-C", root, "-c", "core.hooksPath=", "-c", "user.email=t@t", "-c", "user.name=t"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A clean tree must produce an empty `hook diff`; the previous commit's diff
// belongs to Store, not to the uncommitted-work view (2026-11-10 campaign:
// `kern hook diff` on a clean tree printed HEAD~1).
func TestDiffWorktreeIsEmptyOnCleanTree(t *testing.T) {
	root := gitInit(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "a.txt")
	runGit(t, root, "commit", "-qm", "one")
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "b.txt")
	runGit(t, root, "commit", "-qm", "two")

	t.Chdir(root)

	wt, err := DiffWorktree()
	if err != nil {
		t.Fatalf("DiffWorktree: %v", err)
	}
	if strings.TrimSpace(wt) != "" {
		t.Fatalf("clean tree must report no uncommitted diff, got:\n%s", wt)
	}

	// The history-range default stays intact for the store path.
	history, err := Diff("", "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(history, "+two") {
		t.Fatalf("Diff default should cover HEAD~1..HEAD (the last commit), got:\n%s", history)
	}

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err = DiffWorktree()
	if err != nil {
		t.Fatalf("DiffWorktree after edit: %v", err)
	}
	if !strings.Contains(wt, "+changed") {
		t.Fatalf("uncommitted edit must appear in DiffWorktree, got:\n%s", wt)
	}
}
