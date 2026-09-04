package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/execution"
)

// WorktreeManager manages isolated git worktrees for tasks and agents (spec Section 17 & KernOps).
// It creates ephemeral worktrees detached at HEAD and wires cleanup to protect the main working tree.
type WorktreeManager struct {
	repoRoot string
	baseDir  string
}

// NewWorktreeManager creates a new WorktreeManager for repoRoot.
func NewWorktreeManager(repoRoot string) *WorktreeManager {
	return &WorktreeManager{
		repoRoot: repoRoot,
		baseDir:  filepath.Join(repoRoot, ".kern", "sandboxes"),
	}
}

// RepoRoot returns the repository root managed by this WorktreeManager.
func (m *WorktreeManager) RepoRoot() string {
	return m.repoRoot
}

// Create creates an isolated git worktree for a task, returning the worktree path and cleanup function.
// If git worktree creation fails (e.g. non-git directory or dirty detached branch), it safely falls back to a temp worktree.
func (m *WorktreeManager) Create(taskID string) (string, func(), error) {
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", os.Getpid())
	}
	cleanTaskID := filepath.Base(taskID)
	targetDir := filepath.Join(m.baseDir, cleanTaskID)

	// Ensure parent directories exist
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return CreateWorktree(m.repoRoot)
	}

	// Create detached worktree using git
	cmd := exec.Command("git", "worktree", "add", "--detach", targetDir, "HEAD")
	cmd.Dir = m.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback to CreateWorktree (temp dir) if git worktree fails (e.g. target path conflict or non-git)
		_ = out
		return CreateWorktree(m.repoRoot)
	}

	cleanup := func() {
		rmCmd := exec.Command("git", "worktree", "remove", "--force", targetDir)
		rmCmd.Dir = m.repoRoot
		_ = rmCmd.Run()

		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = m.repoRoot
		_ = pruneCmd.Run()

		_ = os.RemoveAll(targetDir)
	}

	return targetDir, cleanup, nil
}

// CreateExecutionWorktree returns an *execution.Worktree backed by an isolated git worktree.
// It connects Blueprint's sandbox worktree isolation directly to Kern's execution.Worktree.
func (m *WorktreeManager) CreateExecutionWorktree(taskID string) (*execution.Worktree, error) {
	path, cleanup, err := m.Create(taskID)
	if err != nil {
		// Fall back to snapshot-based worktree if git worktree creation fails completely
		return execution.NewWorktree(m.repoRoot)
	}
	return execution.NewWorktreeWithCleaner(m.repoRoot, path, cleanup), nil
}
