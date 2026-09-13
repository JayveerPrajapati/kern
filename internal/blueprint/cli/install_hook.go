package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// runInstallHook implements `blueprint install hook [pre-commit|pre-push|all]`.
// It writes hook scripts to .git/hooks/ (pre-commit, pre-push, or both).
func runInstallHook(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(os.Stderr, `Usage: blueprint install hook [pre-commit|pre-push|all]

Install git hooks for Blueprint change governance:
  pre-commit  Fast staged validation on every commit (default)
  pre-push    Blocking staged validation (secrets, architecture, duplication) before push
  all         Install both pre-commit and pre-push hooks

The deep validation suites (tests:build-test sandbox run, resilience
scenarios) are NOT part of either hook: they run in CI on every PR
(ci.yml: go test -short ./... plus the full non-short blueprint suite,
including the resilience gates) and deeply in blueprint-nightly.yml.
Measured on a 2-file staged change: --tests adds ~135s and --resilience
~120s to the hook, while the blocking legs (secret+architecture+
duplication) complete in ~5s.`)
		return 0
	}

	target := "pre-commit"
	if len(args) > 0 {
		target = args[0]
	}

	switch target {
	case "pre-commit", "pre-push", "all":
	default:
		fmt.Fprintf(os.Stderr, "blueprint: invalid hook target %q (must be pre-commit, pre-push, or all)\n", target)
		return 2
	}

	// Find the git directory.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: cannot determine working directory: %v\n", err)
		return 2
	}
	gitDir, err := findGitDir(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: %v\n", err)
		return 2
	}

	// The pre-commit hook runs in fast mode: the jscpd two-pass duplication
	// scan (the slowest leg) is skipped in favor of advisory in-house findings,
	// so the hook stays ~2s. The full two-pass check runs in CI (or locally
	// with `kern check --staged` without --fast).
	//
	// The pre-push hook keeps every BLOCKING leg (secrets, architecture,
	// approval, full two-pass duplication) but drops the deep suites:
	// --tests (tests:build-test, measured ~135s on a 2-file change) and
	// --resilience (resilience:scenarios, ~120s, advisory findings) are
	// enforced by CI on every PR instead (ci.yml runs go test -short ./...
	// plus the full non-short blueprint suite; blueprint-nightly.yml adds
	// the nightly deep pass). Fail-closed semantics are unchanged: the
	// hook still propagates kern check's exit code, and every blocking
	// check that runs in the hook still blocks the push; only work that CI
	// re-enforces was deferred.
	if target == "pre-commit" || target == "all" {
		if err := installSingleHook(gitDir, "pre-commit", "--staged --fast", "every commit"); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: %v\n", err)
			return 2
		}
		fmt.Println("To bypass pre-commit: git commit --no-verify")
	}

	if target == "pre-push" || target == "all" {
		if err := installSingleHook(gitDir, "pre-push", "--staged", "every git push"); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: %v\n", err)
			return 2
		}
		fmt.Println("To bypass pre-push: git push --no-verify")
		fmt.Println("Deep suites (tests, resilience) run in CI on every PR and nightly (blueprint-nightly.yml).")
	}

	return 0
}

func installSingleHook(gitDir, hookName, checkFlags, desc string) error {
	hookPath := filepath.Join(gitDir, "hooks", hookName)

	// Check if a hook already exists and is NOT our hook.
	if existing, err := os.ReadFile(hookPath); err == nil {
		if !isBlueprintHook(existing) {
			return fmt.Errorf("a %s hook already exists at %s\n  To overwrite, remove it first: rm %s\n  Then re-run: kern install hook %s", hookName, hookPath, hookPath, hookName)
		}
	}

	hooksDir := filepath.Dir(hookPath)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("cannot create hooks directory: %w", err)
	}

	hookContent := fmt.Sprintf("#!/bin/sh\n"+
		"# Blueprint %s hook — change-governance adapter to `kern check %s`\n"+
		"# Installed by `kern check install` (Blueprint CLI, merged into kern).\n"+
		"# Blueprint operations live inside kern (kern check / kern ci / kern sec);\n"+
		"# there is no separate blueprint binary to keep in sync. KERN_BINARY\n"+
		"# overrides the kern executable when it is not on PATH.\n"+
		"exec \"${KERN_BINARY:-kern}\" check %s --format=terminal\n", hookName, checkFlags, checkFlags)

	if err := os.WriteFile(hookPath, []byte(hookContent), 0o755); err != nil {
		return fmt.Errorf("cannot write hook: %w", err)
	}

	if err := os.Chmod(hookPath, 0o755); err != nil {
		return fmt.Errorf("cannot chmod hook: %w", err)
	}

	fmt.Printf("Installed %s hook at %s\n", hookName, hookPath)
	fmt.Printf("The hook runs `kern check %s` on %s.\n", checkFlags, desc)
	return nil
}

// findGitDir walks up from start to find a .git directory or .git file
// (for worktrees).
func findGitDir(start string) (string, error) {
	dir := start
	for i := 0; i < 20; i++ {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath, nil
			}
			// .git is a file (worktree) — read the gitdir pointer and
			// resolve the common git dir so the hook lands where git
			// actually reads it.
			gitDir, err := worktreeGitDir(gitPath)
			if err != nil {
				return "", fmt.Errorf("cannot read gitdir pointer %s: %v", gitPath, err)
			}
			return commonGitDir(gitDir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not a git repository (no .git found walking up from %s)", start)
}

// worktreeGitDir reads the `gitdir: <path>` line from a .git file (a git
// worktree) and resolves it to an absolute path.
func worktreeGitDir(gitFile string) (string, error) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	line := bytes.TrimSpace(data)
	prefix := []byte("gitdir:")
	if !bytes.HasPrefix(line, prefix) {
		return "", fmt.Errorf("malformed .git file: missing gitdir line")
	}
	path := string(bytes.TrimSpace(line[len(prefix):]))
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(gitFile), path)
	}
	return path, nil
}

// commonGitDir returns the common git directory for a worktree gitdir.
// Hooks live in the common dir, not in .git/worktrees/<name>.
func commonGitDir(gitDir string) string {
	const marker = "/.git/worktrees/"
	if idx := indexOfBytes([]byte(gitDir), []byte(marker)); idx >= 0 {
		return gitDir[:idx] + "/.git"
	}
	return gitDir
}

// isBlueprintHook returns true if the existing hook content was installed by
// Blueprint change governance: either a legacy hook written by the standalone
// `blueprint install hook`, or the current merged form written by
// `kern install hook` (content contains a "kern <name> hook" governance
// marker). Any other pre-existing hook is left untouched.
func isBlueprintHook(content []byte) bool {
	return bytes.Contains(content, []byte("Blueprint pre-commit hook")) ||
		bytes.Contains(content, []byte("Blueprint pre-push hook")) ||
		(bytes.Contains(content, []byte("change-governance")) && bytes.Contains(content, []byte("hook")) && bytes.Contains(content, []byte("kern"))) ||
		(bytes.Contains(content, []byte("Blueprint")) && bytes.Contains(content, []byte("hook")))
}

// indexOfBytes reports the index of the first instance of sep in s, or -1.
func indexOfBytes(s, sep []byte) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		match := true
		for j := 0; j < len(sep); j++ {
			if s[i+j] != sep[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
