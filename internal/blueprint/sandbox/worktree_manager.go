package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// staleWorktreeAge is how old an unregistered worktree copy must be before GC
// removes it. Loop/check runs keep their worktree only for the duration of
// the run; anything older that is not a registered git worktree is abandoned
// (interrupted run, crashed process) and safe to delete.
const staleWorktreeAge = 24 * time.Hour

// registeredWorktreeMaxAge is how old a REGISTERED worktree must be
// before GC force-removes it. Registered worktrees are normally active,
// but a killed process leaves git metadata behind forever (the worktree
// stays in `git worktree list` with no live owner). No loop/check run
// legitimately lives this long, so older entries are abandoned.
const registeredWorktreeMaxAge = 7 * 24 * time.Hour

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
	// Sweep abandoned worktree copies from previous interrupted runs before
	// creating a new one (V3: stale snapshots accumulated in user repos).
	_, _ = m.GC(staleWorktreeAge)

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

// GC removes abandoned worktree copies under baseDir: entries older than
// maxAge that are NOT registered git worktrees are deleted, then
// `git worktree prune` runs to drop their metadata. Fresh copies and
// registered (active) worktrees are never touched.
func (m *WorktreeManager) GC(maxAge time.Duration) ([]string, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	active := m.registeredWorktrees()
	cutoff := time.Now().Add(-maxAge)
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(m.baseDir, e.Name())
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		// Normalize symlinked roots (/var -> /private/var on macOS) so the
		// git worktree list comparison is path-identical.
		registered := false
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			registered = active[rp]
		}
		if !registered {
			registered = active[p]
		}
		if registered {
			// Registered worktrees are normally active; only entries older
			// than registeredWorktreeMaxAge are abandoned copies left by a
			// killed process — force-remove them through git so the
			// metadata does not linger.
			regCutoff := time.Now().Add(-registeredWorktreeMaxAge)
			if !info.ModTime().Before(regCutoff) {
				continue
			}
			rm := exec.Command("git", "worktree", "remove", "--force", p)
			rm.Dir = m.repoRoot
			if rm.Run() != nil {
				continue // still in use or git unhappy — leave it
			}
			if err := os.RemoveAll(p); err == nil {
				removed = append(removed, p)
			}
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	if len(removed) > 0 {
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = m.repoRoot
		_ = pruneCmd.Run()
	}
	return removed, nil
}

// registeredWorktrees returns the absolute paths git currently has checked
// out as worktrees (git worktree list --porcelain). Active worktrees are
// never candidates for GC, regardless of age.
func (m *WorktreeManager) registeredWorktrees() map[string]bool {
	out := map[string]bool{}
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = m.repoRoot
	b, err := cmd.CombinedOutput()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			p := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			if rp, err := filepath.EvalSymlinks(p); err == nil {
				p = rp
			}
			out[p] = true
		}
	}
	return out
}
