package setup

import (
	"os/exec"

	"github.com/JayveerPrajapati/kern/internal/bpcli/cli"
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

	if err := cli.InstallGlobalGitHooks(); err != nil {
		return Status{Agent: "global-git-hooks", Installed: false, Path: "~/.kern/git-hooks", Note: err.Error()}
	}
	dir, _ := cli.GlobalGitHooksDir()
	return Status{
		Agent:     "global-git-hooks",
		Installed: true,
		Path:      dir,
		// Honest side-effect note (dogfooding A1-N4): the global hooks run on
		// EVERY commit in every repo — pre-commit gates via `kern check
		// --staged` (governance audit chain + .blueprint/audit) and
		// post-commit stores the diff into project memory. Users must not
		// discover this only after the first commit.
		Note: "global git hooks installed & active across all repos (pre-commit gates + audits, post-commit stores diff into project memory on every commit)",
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

	installed, details := cli.IsGlobalGitHooksInstalled()
	return Status{
		Agent:     "global git hooks",
		Installed: installed,
		Path:      "~/.kern/git-hooks",
		Note:      details,
	}
}
