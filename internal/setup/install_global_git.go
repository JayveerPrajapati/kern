package setup

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

// gitHookChainFunc defines kern_chain_local: run the repository's own hook of
// the same name. A global core.hooksPath makes git ignore <repo>/.git/hooks
// entirely, and `git rev-parse --git-path hooks/X` resolves back to the global
// dir, so the repo's hook is located through --git-common-dir instead.
const gitHookChainFunc = `
kern_chain_local() {
  kern_hook_name=$1
  shift
  kern_common=$(git rev-parse --git-common-dir 2>/dev/null) || return 0
  kern_local_hook="$kern_common/hooks/$kern_hook_name"
  [ -f "$kern_local_hook" ] && [ -x "$kern_local_hook" ] || return 0
  [ "$kern_local_hook" -ef "$0" ] && return 0
  "$kern_local_hook" "$@"
}
`

// globalPassthroughHooks are every other git hook name. Without a shim for
// each, a global core.hooksPath would silently disable the repo's own hook.
var globalPassthroughHooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"prepare-commit-msg", "commit-msg", "pre-merge-commit",
	"pre-rebase", "post-checkout", "post-merge", "post-rewrite",
	"pre-auto-gc", "post-update", "push-to-checkout",
}

const globalPassthroughHookScript = `#!/bin/sh
# Kern pass-through: a global core.hooksPath replaces the repo's hooks
# directory, so forward to the repo's own hook of the same name (stdin and
# arguments preserved).
kern_name=$(basename "$0")
kern_common=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
kern_local_hook="$kern_common/hooks/$kern_name"
[ -f "$kern_local_hook" ] && [ -x "$kern_local_hook" ] || exit 0
[ "$kern_local_hook" -ef "$0" ] && exit 0
exec "$kern_local_hook" "$@"
`

// globalPrePushHookScript blocks the pushes that are almost always mistakes:
// deleting or force-pushing a protected branch (default: main, master;
// override with KERN_PROTECTED_BRANCHES="main release"). Pure git, no kern
// binary needed, so it cannot fail closed on a missing install.
const globalPrePushHookScript = `#!/bin/sh
# Kern Universal Pre-Push Hook — protected-branch guard
# Installed by kern setup / kern install hook --global.

if [ "$KERN_BYPASS" = "1" ] || [ "$KERN_ENFORCE" = "0" ]; then
  reason="${KERN_BYPASS_REASON:-Emergency override active}"
  echo "⚠️  [kern] Emergency bypass active: $reason (push allowed)" >&2
  mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-bypass","reason":"%s","pwd":"%s","hook":"pre-push"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${KERN_BYPASS_REASON:-unset}" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
  exit 0
fi
KERN_CHAIN_PLACEHOLDER
kern_is_zero() {
  case "$1" in *[!0]*) return 1 ;; *) return 0 ;; esac
}

input=$(cat)
protected="${KERN_PROTECTED_BRANCHES:-main master}"

# No silent off-switch here (KERN_DISABLED is deliberately NOT honored): the
# only way past this guard is the audited KERN_BYPASS above.
{
  printf '%s\n' "$input" | {
    rc=0
    while read -r local_ref local_sha remote_ref remote_sha; do
      [ -n "$remote_ref" ] || continue
      branch=${remote_ref#refs/heads/}
      [ "$branch" != "$remote_ref" ] || continue
      is_protected=0
      for p in $protected; do
        [ "$branch" = "$p" ] && is_protected=1
      done
      [ "$is_protected" = "1" ] || continue
      if kern_is_zero "$local_sha"; then
        echo "❌ [kern] Refusing to delete protected branch '$branch'." >&2
        rc=1
      elif ! kern_is_zero "$remote_sha" && ! git merge-base --is-ancestor "$remote_sha" "$local_sha" 2>/dev/null; then
        echo "❌ [kern] Refusing non-fast-forward (force) push to protected branch '$branch'." >&2
        rc=1
      fi
    done
    exit $rc
  }
  status=$?
  if [ $status -ne 0 ]; then
    echo "🚨 Intentional? KERN_BYPASS=1 KERN_BYPASS_REASON=\"...\" git push (audited)" >&2
    exit $status
  fi
}

printf '%s\n' "$input" | kern_chain_local pre-push "$@"
exit $?
`

var globalPreCommitHookScript = strings.ReplaceAll(globalPreCommitHookTemplate, "KERN_CHAIN_PLACEHOLDER", gitHookChainFunc)
var globalPrePushHook = strings.ReplaceAll(globalPrePushHookScript, "KERN_CHAIN_PLACEHOLDER", gitHookChainFunc)

const globalPreCommitHookTemplate = `#!/bin/sh
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

KERN_CHAIN_PLACEHOLDER
# Fail open when kern itself is unavailable (uninstalled, moved): a missing
# binary must never turn into "every commit on this machine is blocked".
if command -v "${KERN_BINARY:-kern}" >/dev/null 2>&1; then
  # Run fast staged validation.
  "${KERN_BINARY:-kern}" verify --staged security,architecture
  status=$?

  if [ $status -ne 0 ]; then
    echo "" >&2
    echo "❌ [kern] Pre-commit validation failed. Commit blocked." >&2
    echo "💡 To fix findings, run: kern verify --staged security,architecture" >&2
    echo "🚨 For emergency bypass: KERN_BYPASS=1 git commit -m \"...\"" >&2
    exit $status
  fi
fi

# Run the repo's own pre-commit hook too (a global core.hooksPath would
# otherwise silently disable it).
kern_chain_local pre-commit "$@"
exit $?
`

var globalPostCommitHookScript = strings.ReplaceAll(globalPostCommitHookTemplate, "KERN_CHAIN_PLACEHOLDER", gitHookChainFunc)

const globalPostCommitHookTemplate = `#!/bin/sh
# Kern Universal Post-Commit Hook — Record commit into engineering memory
# Installed by kern setup / kern install hook --global.
KERN_CHAIN_PLACEHOLDER
if [ "${KERN_DISABLED:-0}" != "1" ] && command -v "${KERN_BINARY:-kern}" >/dev/null 2>&1; then
  "${KERN_BINARY:-kern}" hook store --range "HEAD~1..HEAD" 2>/dev/null || true
fi

kern_chain_local post-commit "$@"
exit $?
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

	prePushPath := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(prePushPath, []byte(globalPrePushHook), 0o755); err != nil {
		return fmt.Errorf("cannot write global pre-push hook %s: %w", prePushPath, err)
	}
	if err := os.Chmod(prePushPath, 0o755); err != nil {
		return fmt.Errorf("cannot chmod global pre-push hook: %w", err)
	}

	for _, name := range globalPassthroughHooks {
		p := filepath.Join(hooksDir, name)
		if err := os.WriteFile(p, []byte(globalPassthroughHookScript), 0o755); err != nil {
			return fmt.Errorf("cannot write global %s hook %s: %w", name, p, err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			return fmt.Errorf("cannot chmod global %s hook: %w", name, err)
		}
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
	if _, err := os.Stat(filepath.Join(hooksDir, "pre-push")); err != nil {
		return false, "pre-push hook missing (re-run: kern setup --global-only)"
	}

	return true, hooksDir
}

// GlobalGitHooksRemoval reports what RemoveGlobalGitHooks did (or, on a dry
// run, would do).
type GlobalGitHooksRemoval struct {
	UnsetHooksPath string   // core.hooksPath value that pointed at kern's dir and was (or would be) unset
	Foreign        string   // a core.hooksPath that is not kern's; left untouched
	Removed        []string // kern-written hook files deleted (or that would be)
	Kept           []string // files in kern's dir that kern did not write; left in place
}

// isKernGlobalHookFile reports whether a hook file's content is one kern wrote.
func isKernGlobalHookFile(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Kern Universal ") || strings.Contains(s, "Kern pass-through")
}

// RemoveGlobalGitHooks undoes InstallGlobalGitHooks: it unsets core.hooksPath
// only when it points at kern's hooks dir (a foreign value is never touched),
// then deletes only the hook files kern wrote there (recognized by content)
// and the dir itself if that leaves it empty. dryRun reports without changes.
func RemoveGlobalGitHooks(dryRun bool) (GlobalGitHooksRemoval, error) {
	var res GlobalGitHooksRemoval
	hooksDir, err := GlobalGitHooksDir()
	if err != nil {
		return res, err
	}
	if _, lerr := exec.LookPath("git"); lerr == nil {
		if out, cerr := exec.Command("git", "config", "--global", "core.hooksPath").Output(); cerr == nil {
			configured := strings.TrimSpace(string(out))
			switch {
			case configured == hooksDir:
				res.UnsetHooksPath = configured
				if !dryRun {
					if uerr := UninstallGlobalGitHooks(); uerr != nil {
						return res, uerr
					}
				}
			case configured != "":
				res.Foreign = configured
			}
		}
	}
	names := append([]string{"pre-commit", "post-commit", "pre-push"}, globalPassthroughHooks...)
	for _, n := range names {
		p := filepath.Join(hooksDir, n)
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		if !isKernGlobalHookFile(b) {
			res.Kept = append(res.Kept, p)
			continue
		}
		if !dryRun {
			if err := os.Remove(p); err != nil {
				return res, fmt.Errorf("cannot remove %s: %w", p, err)
			}
		}
		res.Removed = append(res.Removed, p)
	}
	if !dryRun {
		_ = os.Remove(hooksDir) // only succeeds when empty
	}
	return res, nil
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
