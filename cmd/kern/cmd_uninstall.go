package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	kernroot "github.com/JayveerPrajapati/kern"
	"github.com/JayveerPrajapati/kern/internal/projectstate"
	setupundo "github.com/JayveerPrajapati/kern/internal/setupundo"
)

// runUninstall implements `kern uninstall` — the full reversal of what
// install.sh install + `kern setup` write, closing the install/uninstall
// symmetry gap (install.sh had uninstall; the kern CLI had update but no
// uninstall). Two phases, each with its own preview/consent:
//
//  1. wiring + user data — `kern setup --remove --data` (in-process, via
//     internal/setupundo): MCP, rules, skills, hooks, editor wiring, the
//     ~/AGENTS.md kern block, then ~/.kern, machine secrets and the cache
//  2. binaries + PATH rc lines — the embedded install.sh/ps1 uninstall
//     --prefix-only (offline: the script ships inside the binary; another
//     prefix's lines always survive)
//
// Modes:
//   - default (and --machine, its legacy alias): the full uninstall above
//   - --prefix-only: phase 2 only (the CI / scratch-install mode)
//   - --project-only: phase 1 limited to this repo's wiring (the
//     installation is untouched)
//
// Before phase 2, a kern-managed current root gets the exact project
// cleanup command PRINTED — never executed: an uninstaller removes the
// tool, not repo content, so it behaves identically from any directory.
func runUninstall(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("uninstall: unexpected argument %q — usage: kern uninstall [--prefix-only] [--project-only] [--agents <list>] [--dry-run] [--yes]", args[0])
	}
	if f.machine {
		fmt.Println("note: --machine is now the default behavior (the full uninstall) — PATH cleanup stays prefix-scoped")
	}
	if f.prefixOnly && f.projectOnly {
		fatalUsage("uninstall: --prefix-only removes binaries only, --project-only removes repo wiring only — pick one")
	}
	if f.prefixOnly && f.machine {
		fatalUsage("uninstall: --machine is the full uninstall — it cannot be combined with --prefix-only")
	}

	// Phase 1 — the wiring + data removal, with its own preview and consent.
	// --prefix-only skips it entirely: that mode exists so a CI or scratch
	// prefix can be removed without touching machine state.
	if !f.prefixOnly {
		removeArgs := []string{"--remove"}
		if f.projectOnly {
			removeArgs = append(removeArgs, "--project")
		} else {
			removeArgs = append(removeArgs, "--data")
		}
		for _, passthrough := range [][2]string{
			{"--root", f.root},
			{"--agents", f.agents},
		} {
			if passthrough[1] != "" {
				removeArgs = append(removeArgs, passthrough[0], passthrough[1])
			}
		}
		if f.dryRun {
			removeArgs = append(removeArgs, "--dry-run")
		}
		if f.yes {
			removeArgs = append(removeArgs, "--yes")
		}
		if f.plain {
			removeArgs = append(removeArgs, "--plain")
		}
		if code := setupundo.RunRemove(removeArgs); code != 0 {
			return code
		}
	}

	if f.projectOnly {
		fmt.Println("\nkern uninstall --project-only: repo wiring only — the installed binaries and user-level config are untouched.")
		return 0
	}

	// Current-project handling: informational only. `kern uninstall` never
	// edits files inside a repository — the managed marker earns a printed
	// cleanup command, nothing more.
	if !f.prefixOnly {
		noteProjectWiring(f)
	}

	// Phase 2 — binaries + PATH lines via the installer. The Go CLI always
	// runs it prefix-scoped: phase 1 already did the wiring and data
	// in-process, so the installer only removes the binaries and this
	// prefix's PATH lines (a scratch prefix never strips the real
	// install's lines).
	if f.dryRun {
		fmt.Println("\n[dry-run] would run the embedded install.sh uninstall --prefix-only: removes the kern binaries at the install prefix and its PATH rc lines (other prefixes' lines survive)")
		fmt.Println("Re-install any time: the install.sh one-liner in the README, then `kern setup`.")
		return 0
	}
	fmt.Println("\nkern uninstall: removing the binaries and PATH lines via the embedded installer (offline)")
	if err := runInstallerUninstall(f); err != nil {
		fatal("uninstall: installer failed: %v", err)
	}
	fmt.Println("Re-install any time: the install.sh one-liner in the README, then `kern setup`.")
	return 0
}

// noteProjectWiring prints the exact command for removing the current
// root's project wiring — and does NOTHING else. `kern uninstall` never
// edits files inside a repository: an uninstaller removes the tool (user
// wiring, machine data, binaries, PATH lines), not project content, and
// must behave identically no matter which directory it runs from. The
// earlier consented-offer design proved destructive in practice (an
// uninstall run from a source repo stripped its own tracked wiring).
// Project wiring (.kern, .mcp.json, AGENTS.md blocks) is removed only by
// the explicit, repo-scoped command below, run deliberately.
func noteProjectWiring(f flags) {
	root := f.root
	if root == "" {
		root = "."
	}
	state := projectstate.Inspect(root)
	if !state.Managed {
		return
	}
	cmd := fmt.Sprintf("kern setup --remove --project --root %s", state.Root)
	fmt.Printf("\n%s is a kern-managed repository — untouched by uninstall (kern never edits repo files). Remove its wiring yourself if wanted:\n  %s\n", state.Root, cmd)
}

// runInstallerUninstall delegates the binary/PATH removal to install.sh
// (unix) or install.ps1 (windows) — the EMBEDDED copy from the module-root
// package (installer_embed.go), so uninstall is fully offline: no curl, no
// network, and the script version always matches the running binary. The
// removal logic still lives in ONE place (the installer scripts, pinned by
// their own tests). Always prefix-scoped: the wiring and data were already
// removed in phase 1.
func runInstallerUninstall(f flags) error {
	goos := runtime.GOOS
	name, content := kernroot.InstallerScript(goos)
	tmp, err := os.CreateTemp("", "kern-uninstall-*-"+name)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if goos == "windows" {
		ps, err := exec.LookPath("pwsh")
		if err != nil {
			if ps, err = exec.LookPath("powershell"); err != nil {
				return fmt.Errorf("PowerShell is required to run the embedded uninstaller (write %s and run it yourself)", tmp.Name())
			}
		}
		install := exec.Command(ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", tmp.Name(), "-Action", "uninstall", "-PrefixOnly")
		install.Stdin = os.Stdin
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		return install.Run()
	}
	args := []string{tmp.Name(), "uninstall", "--prefix-only"}
	if f.yes {
		args = append(args, "--yes")
	}
	install := exec.Command("sh", args...)
	// The script's consent prompt reads stdin: the child must inherit the
	// terminal. (The old curl pipe masked this — stdin WAS the script
	// stream, so an interactive run could only ever work with --yes.)
	install.Stdin = os.Stdin
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	return install.Run()
}
