package setupundo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// ui.go — the `kern setup --remove [--project]` command flow, moved from
// internal/setup with the removal family extraction. RunRemove is invoked
// via TryRemove from cmd/kern's setup dispatch (setup itself never imports
// this package, so the family stays acyclic).

type exitError struct{ code int }

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and signals exit code 2 (the Unix
// convention for usage errors). It must never print a stack trace.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// run executes a command body, recovering the exitError sentinel and
// returning its code. Non-sentinel panics are re-panicked.
func run(body func()) int {
	code := 0
	func() {
		defer func() {
			if r := recover(); r != nil {
				e, ok := r.(exitError)
				if !ok {
					panic(r)
				}
				code = e.code
			}
		}()
		body()
	}()
	return code
}

// removeFlags carries every setup flag the removal flow must know about:
// its own flags plus the wiring flags it must reject (and the read-only
// modes it defers to), so combinations behave exactly as they did before
// the extraction.
type removeFlags struct {
	root, agents, agentsMD string
	project                bool
	global                 bool
	globalOnly             bool
	globalRules            bool
	allAgents              bool
	detect                 bool
	check                  bool
	verify                 bool
	dryRun                 bool
	hooksOnly              bool
	data                   bool
	plain                  bool
	yes                    bool
}

func parseRemoveFlags(args []string) (removeFlags, []string, error) {
	var f removeFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--remove", "--json":
			// --remove presence was established by TryRemove; --json was a
			// no-op on the removal path before the extraction too.
		case "--project":
			p.SetBool(&f.project, "--project", inline, hasInline)
		case "--agents":
			p.SetStr(&f.agents, inline, hasInline)
		case "--agents-md":
			p.SetStr(&f.agentsMD, inline, hasInline)
		case "--global":
			p.SetBool(&f.global, "--global", inline, hasInline)
		case "--global-only":
			p.SetBool(&f.globalOnly, "--global-only", inline, hasInline)
		case "--global-rules":
			p.SetBool(&f.globalRules, "--global-rules", inline, hasInline)
		case "--all-agents":
			p.SetBool(&f.allAgents, "--all-agents", inline, hasInline)
		case "--detect":
			p.SetBool(&f.detect, "--detect", inline, hasInline)
		case "--check":
			p.SetBool(&f.check, "--check", inline, hasInline)
		case "--verify":
			p.SetBool(&f.verify, "--verify", inline, hasInline)
		case "--dry-run":
			p.SetBool(&f.dryRun, "--dry-run", inline, hasInline)
		case "--hooks-only":
			p.SetBool(&f.hooksOnly, "--hooks-only", inline, hasInline)
		case "--data":
			p.SetBool(&f.data, "--data", inline, hasInline)
		case "--plain":
			p.SetBool(&f.plain, "--plain", inline, hasInline)
		case "--yes":
			p.SetBool(&f.yes, "--yes", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
			// Consumed by resolveCommandAndFlags before dispatch; tolerated
			// defensively like the unified parser.
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// TryRemove claims a `kern setup` invocation when it is a removal request:
// --remove is present and neither read-only mode (--check/--verify, which
// took precedence over --remove before the extraction) is. Every other
// invocation reports ok=false so cmd/kern hands the args to setup.RunSetup
// untouched.
func TryRemove(rest []string) (int, bool) {
	var f removeFlags
	var removeFlag bool
	p := cliflags.NewParser(rest)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--remove":
			p.SetBool(&removeFlag, "--remove", inline, hasInline)
		case "--check":
			p.SetBool(&f.check, "--check", inline, hasInline)
		case "--verify":
			p.SetBool(&f.verify, "--verify", inline, hasInline)
		}
	}
	if !removeFlag || f.check || f.verify {
		return 0, false
	}
	return RunRemove(rest), true
}

// RunRemove implements `kern setup --remove [--project]` end to end and
// returns the process exit code.
func RunRemove(rest []string) int {
	return run(func() { runRemove(rest) })
}

// runRemove implements the machine-wide undo, or with --project the
// repository-scope undo (the project files `kern setup` wrote inside the
// root).
func runRemove(rest []string) {
	f, pos, err := parseRemoveFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(pos) > 0 {
		// A positional here is almost certainly a user guessing a
		// subcommand ("kern setup status" reads like a read-only query);
		// running the real removal under that intent would be worse.
		fatalUsage("setup: unexpected argument %q — usage: kern setup [flags]; the read-only wiring check is `kern setup --check`", pos[0])
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if f.project {
		if f.global || f.globalOnly || f.globalRules || f.allAgents || f.detect || f.agentsMD != "" {
			fatalUsage("setup: --remove --project is repo-scoped — it cannot be combined with --global, --global-only, --global-rules, --all-agents, --detect or --agents-md")
		}
	} else if f.global || f.globalOnly || f.globalRules || f.allAgents || f.detect || f.agentsMD != "" {
		fatalUsage("setup: --remove cannot be combined with --global, --global-only, --global-rules, --all-agents, --detect or --agents-md (use --agents to limit what is removed; add --project to undo the repo's own wiring)")
	}
	if f.data {
		if f.project {
			fatalUsage("setup: --remove --data removes user-level state only — a repository's own .kern stays until you run `kern setup --remove --project`")
		}
		if f.hooksOnly {
			fatalUsage("setup: --remove --data removes ~/.kern, machine secrets and caches — it cannot be combined with the --hooks-only escape hatch")
		}
	}
	var agents []string
	for _, a := range strings.Split(f.agents, ",") {
		if a = strings.TrimSpace(a); a != "" {
			agents = append(agents, a)
		}
	}
	ui := setup.NewTermStyle(os.Stdout, f.plain)
	remove, title, noneWhere := RemoveGlobal, "kern setup --remove", "no kern wiring found in user-level config"
	intro := []string{
		"Removes only kern's own entries from your user-level config.",
		"Each edited file is backed up first; ~/.kern data is never touched.",
	}
	if f.data {
		intro = []string{
			"Removes kern's user-level wiring AND the user-data directories:",
			"~/.kern (indexes, approvals, audit, memory), machine secrets,",
			"and the kern cache — the data half of a full `kern uninstall`.",
			"Each edited file is backed up first. Reinstalling starts empty.",
		}
	}
	scope := "all agents, machine-wide"
	if f.hooksOnly {
		intro = []string{
			"Removes only kern's agent/git hooks from your user-level config.",
			"MCP, rules, skills, editor wiring, and ~/.kern data stay in place.",
		}
		scope = "hooks only, machine-wide"
	}
	if f.project {
		remove, title, noneWhere = RemoveProject, "kern setup --remove --project", "no kern wiring found in this repository"
		intro = []string{
			"Removes only kern's own wiring from this repository.",
			"Each edited file is backed up first; .kern data is never touched.",
		}
		scope = "the whole repository"
		if f.hooksOnly {
			intro = []string{
				"Removes only kern's project agent hooks.",
				"MCP, rules, skills, git excludes, and .kern data stay in place.",
			}
			scope = "project hooks only"
		}
	}
	if len(agents) > 0 {
		scope = "agents: " + strings.Join(agents, ", ")
	}
	intro = append(intro, "Scope: "+scope)
	if ui.Rich {
		fmt.Print(ui.Box(title, intro))
	} else {
		fmt.Printf("%s (%s)\n", title, scope)
	}

	preview := remove(root, RemoveOptions{Agents: agents, DryRun: true, HooksOnly: f.hooksOnly})
	if f.data {
		preview = append(preview, RemoveMachineData(true)...)
	}
	if len(preview) == 0 {
		fmt.Println("Nothing to remove: " + noneWhere + ".")
		return
	}
	failed := 0
	for _, s := range preview {
		if removeMark(s) == "!!" {
			failed++
		}
	}
	if failed > 0 {
		for _, s := range preview {
			setup.PrintSetupStatus(ui, removeMark(s), s)
		}
		fatal("setup --remove: %d problem(s) above — nothing was changed", failed)
	}
	if ui.Rich {
		fmt.Print(ui.Heading(1, 2, "Plan"))
	}
	for _, s := range preview {
		setup.PrintSetupStatus(ui, removeMark(s), s)
	}
	if f.dryRun {
		fmt.Println("\nDry-run preview only — nothing was changed. Re-run without --dry-run to remove.")
		return
	}
	if !f.yes {
		if !setup.IsTTY(os.Stdin) {
			fatalUsage("setup --remove: refusing to modify your config non-interactively without --yes (preview with --dry-run)")
		}
		if !confirmRemove(ui, os.Stdin, "Remove these kern entries?") {
			fmt.Println("Cancelled. Nothing was changed.")
			return
		}
	}
	if ui.Rich {
		fmt.Print(ui.Heading(2, 2, "Removing"))
	}
	removed := 0
	executed := remove(root, RemoveOptions{Agents: agents, HooksOnly: f.hooksOnly})
	if f.data {
		executed = append(executed, RemoveMachineData(false)...)
	}
	for _, s := range executed {
		mark := removeMark(s)
		switch mark {
		case "ok":
			removed++
		case "!!":
			failed++
		}
		setup.PrintSetupStatus(ui, mark, s)
	}
	if f.project {
		if f.hooksOnly {
			fmt.Printf("\nRemoved kern hooks from %d project location(s) in %s.\n", removed, root)
			fmt.Println("Left untouched: project MCP, rules, skills, git excludes, .kern data, and user-level config.")
		} else {
			fmt.Printf("\nRemoved kern's project wiring from %d location(s) in %s.\n", removed, root)
			fmt.Println("Left untouched: .kern data (index, approvals, the persisted agents_md choice) and your user-level config (`kern setup --remove` without --project).")
		}
		fmt.Println("Re-install any time: kern setup")
	} else {
		if f.hooksOnly {
			fmt.Printf("\nRemoved kern hooks from %d location(s). Restart your agents to drop hook enforcement.\n", removed)
			fmt.Println("Left untouched: ~/.kern data, MCP wiring, rules, skills, editor config, and project files.")
			fmt.Println("Re-install hooks any time: kern setup --global-only")
		} else if f.data {
			fmt.Printf("\nRemoved kern wiring and user data from %d location(s). Restart your agents to drop the MCP server and hooks.\n", removed)
			fmt.Println("Left untouched: any project files `kern setup` wrote inside repos (undo those with `kern setup --remove --project`).")
			fmt.Println("Re-install any time: the install.sh one-liner, then `kern setup`.")
		} else {
			fmt.Printf("\nRemoved kern from %d location(s). Restart your agents to drop the MCP server and hooks.\n", removed)
			fmt.Println("Left untouched: ~/.kern data (index, audit, memory) and any project files `kern setup` wrote inside repos (undo those with `kern setup --remove --project`).")
			fmt.Println("Re-install any time: kern setup --global-only --global-rules")
		}
	}
	if failed > 0 {
		fatal("setup --remove: %d step(s) failed — see the errors above", failed)
	}
}

// confirmRemove asks a yes/no question where the default is NO: removal edits
// the user's own config, so only an explicit y/yes proceeds.
func confirmRemove(t setup.TermStyle, in io.Reader, q string) bool {
	fmt.Printf("\n%s %s ", t.Bold(q), t.Dim("[y/N]"))
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func removeMark(s setup.Status) string {
	switch {
	case s.Skipped:
		return "--"
	case !s.Installed:
		return "!!"
	}
	return "ok"
}
