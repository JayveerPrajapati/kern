package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module d\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return root
}

// chdirRepo swaps the process CWD into a git repo (Diff/Store use CWD for git),
// restoring the original on cleanup. Non-parallel only.
func chdirRepo(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

func TestDiffBetweenCommits(t *testing.T) {
	root := gitRepo(t)
	// Second commit with a change.
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "second")
	chdirRepo(t, root)

	out, err := Diff("HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected main.go in diff, got %q", out)
	}
}

func TestDiffDefaultRange(t *testing.T) {
	root := gitRepo(t)
	// Modify a tracked file (working tree change), default range (HEAD~1..HEAD)
	// cannot resolve in a 1-commit repo -> falls back to working-tree diff.
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(2) }\n"), 0o644)
	chdirRepo(t, root)

	out, err := Diff("", "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected working-tree change via fallback, got %q", out)
	}
}

func TestDiffFailsOutsideRepo(t *testing.T) {
	// CWD is not a git repo: both the ranged and working-tree diffs fail.
	dir := t.TempDir()
	chdirRepo(t, dir)
	if _, err := Diff("HEAD~1", "HEAD"); err == nil {
		t.Fatal("expected error outside a git repo")
	}
}

func TestStoreRecordsChange(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := gitRepo(t)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(3) }\n"), 0o644)
	chdirRepo(t, root)
	if err := Store(root, "", ""); err != nil {
		t.Fatalf("Store: %v", err)
	}
	entries := memory.List(root)
	if len(entries) == 0 {
		t.Fatal("expected a memory entry from Store")
	}
	if !strings.Contains(entries[0].Text, "latest change:") {
		t.Fatalf("expected change stored in memory, got %q", entries[0].Text)
	}
}

func TestStoreEmptyDiffNoOp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := gitRepo(t)
	chdirRepo(t, root)
	// Clean tree: default range HEAD~1..HEAD errors in a fresh single-commit
	// repo; working-tree fallback yields no changes -> Store is a no-op.
	if err := Store(root, "", ""); err != nil {
		t.Fatalf("Store on empty diff should not error: %v", err)
	}
	if len(memory.List(root)) != 0 {
		t.Fatal("expected no memory for empty diff")
	}
}
