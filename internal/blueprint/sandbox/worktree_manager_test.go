package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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

// TestWorktreeManagerGC verifies GC removes abandoned (old, unregistered)
// worktree copies while keeping fresh copies and registered worktrees
// (V3: stale snapshots accumulated in user repos).
func TestWorktreeManagerGC(t *testing.T) {
	tmp := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init not available: %v (%s)", err, string(out))
	}
	exec.Command("git", "-C", tmp, "config", "user.email", "test@example.com").Run()
	exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Test Repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", tmp, "add", ".").Run()
	exec.Command("git", "-C", tmp, "commit", "-m", "initial commit").Run()

	mgr := NewWorktreeManager(tmp)
	base := filepath.Join(tmp, ".kern", "sandboxes")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1) an abandoned copy: old + unregistered
	staleDir := filepath.Join(base, "stale-run")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(staleDir, old, old); err != nil {
		t.Fatal(err)
	}

	// 2) a fresh copy: young, unregistered -> kept
	freshDir := filepath.Join(base, "fresh-run")
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(freshDir, "b.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3) a registered worktree: old but active -> kept
	regDir := filepath.Join(base, "active-task")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := exec.Command("git", "-C", tmp, "worktree", "add", "--detach", regDir, "HEAD")
	if out, err := wt.CombinedOutput(); err != nil {
		t.Fatalf("worktree add failed: %v (%s)", err, string(out))
	}
	if err := os.Chtimes(regDir, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := mgr.GC(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != staleDir {
		t.Fatalf("GC removed %v, want only the stale dir %s", removed, staleDir)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale dir still exists after GC")
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh dir was removed: %v", err)
	}
	if _, err := os.Stat(regDir); err != nil {
		t.Fatalf("registered worktree was removed: %v", err)
	}
}
