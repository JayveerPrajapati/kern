package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireKernPath resolves the kern binary for gate tests that spawn the
// blueprint binary with a KERN_BINARY env override. Resolution order:
//
//  1. KERN_BINARY environment variable
//  2. kern on $PATH (exec.LookPath)
//  3. ../kern/bin/kern relative to the repo root, if present and executable
//
// It returns the absolute path, or skips the test when no candidate exists —
// so gate tests that genuinely need kern run on any machine where kern is
// reachable through that chain, not only when KERN_BINARY happens to be set.
func requireKernPath(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("KERN_BINARY"); p != "" {
		return absOr(p)
	}
	if p, err := exec.LookPath("kern"); err == nil {
		return absOr(p)
	}
	for _, candidate := range []string{
		filepath.Join(findRepoRoot(t), "bin", "kern"),
		filepath.Join(findRepoRoot(t), "..", "kern", "bin", "kern"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	t.Skipf("kern binary not available (tried env, PATH, bin/kern, ../kern/bin/kern)")
	return ""
}

// absOr returns an absolute form of p, falling back to p itself.
func absOr(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
