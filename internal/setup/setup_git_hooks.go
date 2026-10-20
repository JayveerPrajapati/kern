package setup

import (
	"os/exec"
)

// wireGlobalGitHooks installs Kern's pre-commit and post-commit hooks globally
// in ~/.kern/git-hooks and sets git config --global core.hooksPath.
func wireGlobalGitHooks() Status {
	if _, err := exec.LookPath("git"); err != nil {
		return Status{
			Agent:   "global-git-hooks",
			Skipped: true,
			Path:    "~/.kern/git-hooks",
			Note:    "git not found on PATH; global git hooks skipped",
		}
	}

	if err := InstallGlobalGitHooks(); err != nil {
		return Status{Agent: "global-git-hooks", Installed: false, Path: "~/.kern/git-hooks", Note: err.Error()}
	}
	dir, _ := GlobalGitHooksDir()
	return Status{
		Agent:     "global-git-hooks",
		Installed: true,
		Path:      dir,
		// Side-effect note: the global hooks run on
		// EVERY commit in every repo — pre-commit gates via `kern check
		// --staged` (governance audit chain + .blueprint/audit) and
		// post-commit stores the diff into project memory. Users must not
		// discover this only after the first commit.
		Note: "global git hooks installed & active across all repos (pre-commit gates + audits, pre-push blocks force-push/delete of main/master, post-commit stores diff into project memory; each repo's own hooks still run)",
	}
}

// checkGlobalGitHooks checks whether global git hooks are active.
func checkGlobalGitHooks() Status {
	if _, err := exec.LookPath("git"); err != nil {
		return Status{
			Agent:   "global git hooks",
			Skipped: true,
			Path:    "~/.kern/git-hooks",
			Note:    "git not found on PATH",
		}
	}

	installed, details := IsGlobalGitHooksInstalled()
	return Status{
		Agent:     "global git hooks",
		Installed: installed,
		Path:      "~/.kern/git-hooks",
		Note:      details,
	}
}
