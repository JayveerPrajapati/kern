package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiffSkipsGitignoredSections reproduces the ops-cockpit diff blowup: a
// worktree snapshot skips sandbox.SkipDirs at any depth (e.g.
// .opencode/node_modules), so `git diff --no-index` reports those trees as
// deleted; a setup-generated gitignored file (e.g. .cursor/instructions) also
// shows up as noise. Both must be dropped from the diff so it reflects only
// real change surface.
func TestDiffSkipsGitignoredSections(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "qa@test")
	runGit(t, root, "config", "user.name", "QA")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "main.go")
	runGit(t, root, "commit", "-qm", "init")

	// Repo ignores a setup-generated dir and a nested dependency tree, both
	// present in the working tree (like .cursor/ and .opencode/node_modules).
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".cursor/\n.opencode/node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".cursor/instructions/kern.mdc", ".opencode/node_modules/pkg/index.js"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("generated"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	wt, err := NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Cleanup() }()
	d, err := wt.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d, ".cursor") || strings.Contains(d, ".opencode") {
		t.Fatalf("diff leaks gitignored sections:\n%s", d)
	}
	if strings.Contains(d, "main.go") {
		t.Fatalf("diff contains unchanged tracked file main.go:\n%s", d)
	}
}

// TestDiffKeepsRealChanges ensures the ignore filter does not swallow actual
// modifications to tracked files.
func TestDiffKeepsRealChanges(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "qa@test")
	runGit(t, root, "config", "user.name", "QA")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "main.go")
	runGit(t, root, "commit", "-qm", "init")

	// Real change surface = worktree diverges from source (a code-stage
	// step modified the copy). Verify the ignore filter does not swallow it.
	wt, err := NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Cleanup() }()
	if err := os.WriteFile(filepath.Join(wt.Dir(), "main.go"), []byte("package main\nfunc main() { println(\"v2\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := wt.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "println") {
		t.Fatalf("diff dropped a real change:\n%s", d)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
