// Package root centralizes workspace-root resolution for MCP tool packages.
package root

import (
	"os"
	"path/filepath"
)

// ResolveRoot returns the cleaned absolute workspace root: cwd when root is
// empty, filepath.Abs+Clean otherwise, falling back to the raw input if
// Abs fails.
func ResolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// PlausibleProject reports whether dir looks like a project the cwd
// fallback may safely target. Two signals count:
//
//   - a real built kern index at the dir itself (.kern/index.json — a
//     populated workspace), or
//   - git discovery: an ancestor directory carrying a REAL repository —
//     .git must be a directory containing a HEAD file (git's own
//     work-tree check, LOW-7): a .git FILE (submodule/worktree pointer)
//     or an empty .git directory no longer enables the fallback, so a
//     planted /tmp/.git cannot fool the server into treating a scratch
//     cwd as a project context.
//
// .kern/index.sqlite is deliberately NOT counted: the server's own
// state machinery (audit/D1 store init) creates it in whatever cwd it
// starts in, so counting it would let one rejected call bootstrap the
// fallback for the next (M#18). Root-less tool calls resolve to the
// server cwd only when this holds; a scratch cwd must produce a
// correctable error, not a silently built junk index.
func PlausibleProject(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "index.json")); err == nil {
		return true
	}
	for d := filepath.Clean(dir); ; {
		if isGitWorkTree(d) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// isGitWorkTree reports whether dir carries a real git repository:
// .git must be a directory containing a HEAD file (git's own check that
// a work tree is backed by a repository). A .git file or an empty .git
// directory does not count (LOW-7).
func isGitWorkTree(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(gitDir, "HEAD"))
	return err == nil
}
