// Package hooks wires kern into git: a post-commit hook compresses each new
// commit's diff into the project's cross-session memory, so agents inherit
// "what changed" without reading the full history.
package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

const maxDiffLines = 200

// Install writes a post-commit hook into the repo at root.
func Install(root string) error {
	if !isGitRepo(root) {
		return fmt.Errorf("%s is not a git repository", root)
	}
	hookDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	hook := filepath.Join(hookDir, "post-commit")
	script := fmt.Sprintf(`#!/bin/sh
# kern: compress the new commit's diff into project memory (installed by kern hook install)
"%s" hook store --range "HEAD~1..HEAD" >/dev/null 2>&1 || true
`, kernBinPath())
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		return err
	}
	return nil
}

func isGitRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func kernBinPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "kern"
	}
	abs := filepath.Join(filepath.Dir(exe), "kern")
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return "kern"
}

// Diff returns the compressed diff for from..to (defaults HEAD~1..HEAD),
// keeping only file headers, hunk headers and added/removed lines.
func Diff(from, to string) (string, error) {
	if from == "" {
		from = "HEAD~1"
	}
	if to == "" {
		to = "HEAD"
	}
	cmd := exec.Command("git", "diff", "--unified=0", from+".."+to)
	out, err := cmd.Output()
	if err != nil {
		// No commits yet: fall back to working-tree changes.
		out, err = exec.Command("git", "diff").Output()
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
	}
	return compressDiff(string(out)), nil
}

func compressDiff(d string) string {
	var b strings.Builder
	n := 0
	lines := strings.Split(d, "\n")
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "diff --git"):
			b.WriteString("## " + strings.TrimPrefix(l, "diff --git ") + "\n")
			n++
		case strings.HasPrefix(l, "@@"):
			b.WriteString(l + "\n")
			n++
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			b.WriteString(l + "\n")
			n++
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			b.WriteString(l + "\n")
			n++
		}
		if n >= maxDiffLines {
			b.WriteString("… (diff truncated)\n")
			break
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Store compresses the diff and records it in the project's memory.
func Store(root, from, to string) error {
	d, err := Diff(from, to)
	if err != nil {
		return err
	}
	if strings.TrimSpace(d) == "" {
		return nil
	}
	return memory.Add(root, "latest change:\n"+d)
}
