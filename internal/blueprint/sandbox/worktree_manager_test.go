package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeManager(t *testing.T) {
	// Create a temp git repo for testing
	tmp := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init not available: %v (%s)", err, string(out))
	}
	// Configure git author
	exec.Command("git", "-C", tmp, "config", "user.email", "test@example.com").Run()
	exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

	// Create an initial commit
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Test Repo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	exec.Command("git", "-C", tmp, "add", ".").Run()
	cmdCommit := exec.Command("git", "-C", tmp, "commit", "-m", "initial commit")
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v (%s)", err, string(out))
	}

	mgr := NewWorktreeManager(tmp)
	if mgr.RepoRoot() != tmp {
		t.Fatalf("expected RepoRoot %s, got %s", tmp, mgr.RepoRoot())
	}

	wt, err := mgr.CreateExecutionWorktree("test-task-1")
	if err != nil {
		t.Fatalf("CreateExecutionWorktree: %v", err)
	}
	if wt == nil || wt.Dir() == "" {
		t.Fatalf("expected non-empty wt")
	}

	// Verify the worktree has README.md
	content, err := os.ReadFile(filepath.Join(wt.Dir(), "README.md"))
	if err != nil {
		t.Fatalf("failed to read README.md from worktree: %v", err)
	}
	if string(content) != "# Test Repo\n" {
		t.Fatalf("unexpected content in worktree: %s", string(content))
	}

	// Cleanup should remove the worktree cleanly
	if err := wt.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}
