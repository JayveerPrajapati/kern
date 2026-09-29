package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GlobalGitHooksDir returns the path where global Kern git hooks live: ~/.kern/git-hooks.
func GlobalGitHooksDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return filepath.Join(home, ".kern", "git-hooks"), nil
}

const globalPreCommitHookScript = `#!/bin/sh
# Kern Universal Pre-Commit Hook — Global Change Governance
# Installed by kern setup / kern install hook --global.
# Gates every git commit machine-wide across all repositories.

# Emergency break-glass bypass — env-only, deliberately: no marker files
# (an agent could touch them to silently disable governance machine-wide).
if [ "$KERN_BYPASS" = "1" ] || [ "$KERN_ENFORCE" = "0" ]; then
  reason="${KERN_BYPASS_REASON:-Emergency override active}"
  echo "⚠️  [kern] Emergency bypass active: $reason (commit allowed)" >&2
  # Best-effort machine-wide audit trail: a failed append must never block
  # the bypassed commit (redirects + 2>/dev/null keep this POSIX-sh safe).
  mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-bypass","reason":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${KERN_BYPASS_REASON:-unset}" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
  exit 0
fi

# Run fast staged validation.
"${KERN_BINARY:-kern}" check --staged --fast --format=terminal
status=$?

if [ $status -ne 0 ]; then
  echo "" >&2
  echo "❌ [kern] Pre-commit validation failed. Commit blocked." >&2
  echo "💡 To fix findings, run: kern check --staged" >&2
  echo "🚨 For emergency bypass: KERN_BYPASS=1 git commit -m \"...\"" >&2
  exit $status
fi

# If repo has a local pre-commit hook that is not this global hook, run it too:
git_dir=$(git rev-parse --git-path hooks/pre-commit 2>/dev/null)
if [ -n "$git_dir" ] && [ -x "$git_dir" ] && [ "$git_dir" != "$0" ]; then
  "$git_dir" "$@"
  exit $?
fi

exit 0
`

const globalPostCommitHookScript = `#!/bin/sh
# Kern Universal Post-Commit Hook — Record commit into engineering memory
# Installed by kern setup / kern install hook --global.

"${KERN_BINARY:-kern}" hook store --range "HEAD~1..HEAD" 2>/dev/null || true

# If repo has a local post-commit hook that is not this global hook, run it too:
git_dir=$(git rev-parse --git-path hooks/post-commit 2>/dev/null)
if [ -n "$git_dir" ] && [ -x "$git_dir" ] && [ "$git_dir" != "$0" ]; then
  "$git_dir" "$@"
  exit $?
fi

exit 0
`

// InstallGlobalGitHooks writes global hooks to ~/.kern/git-hooks and sets
// `git config --global core.hooksPath ~/.kern/git-hooks`.
func InstallGlobalGitHooks() error {
	hooksDir, err := GlobalGitHooksDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("cannot create global hooks directory %s: %w", hooksDir, err)
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	if err := os.WriteFile(preCommitPath, []byte(globalPreCommitHookScript), 0o755); err != nil {
		return fmt.Errorf("cannot write global pre-commit hook %s: %w", preCommitPath, err)
	}
	if err := os.Chmod(preCommitPath, 0o755); err != nil {
		return fmt.Errorf("cannot chmod global pre-commit hook: %w", err)
	}

	postCommitPath := filepath.Join(hooksDir, "post-commit")
	if err := os.WriteFile(postCommitPath, []byte(globalPostCommitHookScript), 0o755); err != nil {
		return fmt.Errorf("cannot write global post-commit hook %s: %w", postCommitPath, err)
	}
	if err := os.Chmod(postCommitPath, 0o755); err != nil {
		return fmt.Errorf("cannot chmod global post-commit hook: %w", err)
	}

	// Set git config --global core.hooksPath
	cmd := exec.Command("git", "config", "--global", "core.hooksPath", hooksDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set git config --global core.hooksPath %s: %v (%s)", hooksDir, err, string(out))
	}

	return nil
}

// IsGlobalGitHooksInstalled checks whether git's global core.hooksPath is set
// to ~/.kern/git-hooks and the pre-commit script exists.
func IsGlobalGitHooksInstalled() (bool, string) {
	hooksDir, err := GlobalGitHooksDir()
	if err != nil {
		return false, err.Error()
	}

	cmd := exec.Command("git", "config", "--global", "core.hooksPath")
	out, err := cmd.Output()
	if err != nil {
		return false, "core.hooksPath not configured"
	}

	configuredPath := strings.TrimSpace(string(out))
	if configuredPath != hooksDir {
		return false, fmt.Sprintf("core.hooksPath points to %s (expected %s)", configuredPath, hooksDir)
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	if _, err := os.Stat(preCommitPath); err != nil {
		return false, "pre-commit hook file missing"
	}

	return true, hooksDir
}

// UninstallGlobalGitHooks clears git config --global core.hooksPath if it points
// to ~/.kern/git-hooks.
func UninstallGlobalGitHooks() error {
	hooksDir, err := GlobalGitHooksDir()
	if err != nil {
		return err
	}

	cmd := exec.Command("git", "config", "--global", "core.hooksPath")
	out, err := cmd.Output()
	if err == nil && bytes.Equal(bytes.TrimSpace(out), []byte(hooksDir)) {
		unsetCmd := exec.Command("git", "config", "--global", "--unset", "core.hooksPath")
		if unOut, unErr := unsetCmd.CombinedOutput(); unErr != nil {
			return fmt.Errorf("failed to unset git config --global core.hooksPath: %v (%s)", unErr, string(unOut))
		}
	}
	return nil
}
