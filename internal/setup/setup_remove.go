package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// confirmRemove asks a yes/no question where the default is NO: removal edits
// the user's own config, so only an explicit y/yes proceeds.
func confirmRemove(t TermStyle, in io.Reader, q string) bool {
	fmt.Printf("\n%s %s ", t.Bold(q), t.Dim("[y/N]"))
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func removeMark(s Status) string {
	switch {
	case s.Skipped:
		return "--"
	case !s.Installed:
		return "!!"
	}
	return "ok"
}

// runSetupRemove implements `kern setup --remove`: the machine-wide undo.
func runSetupRemove(f setupFlags, root string) {
	if f.global || f.globalOnly || f.globalRules || f.allAgents || f.detect || f.agentsMD != "" {
		fatalUsage("setup: --remove cannot be combined with --global, --global-only, --global-rules, --all-agents, --detect or --agents-md (use --agents to limit what is removed)")
	}
	var agents []string
	for _, a := range strings.Split(f.agents, ",") {
		if a = strings.TrimSpace(a); a != "" {
			agents = append(agents, a)
		}
	}
	ui := NewTermStyle(os.Stdout, f.plain)
	scope := "all agents, machine-wide"
	if len(agents) > 0 {
		scope = "agents: " + strings.Join(agents, ", ")
	}
	if ui.Rich {
		fmt.Print(ui.Box("kern setup --remove", []string{
			"Removes only kern's own entries from your user-level config.",
			"Each edited file is backed up first; ~/.kern data is never touched.",
			"Scope: " + scope,
		}))
	} else {
		fmt.Printf("kern setup --remove (%s)\n", scope)
	}

	preview := RemoveGlobal(root, RemoveOptions{Agents: agents, DryRun: true})
	if len(preview) == 0 {
		fmt.Println("Nothing to remove: no kern wiring found in user-level config.")
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
			PrintSetupStatus(ui, removeMark(s), s)
		}
		fatal("setup --remove: %d problem(s) above — nothing was changed", failed)
	}
	if ui.Rich {
		fmt.Print(ui.Heading(1, 2, "Plan"))
	}
	for _, s := range preview {
		PrintSetupStatus(ui, removeMark(s), s)
	}
	if f.dryRun {
		fmt.Println("\nDry-run preview only — nothing was changed. Re-run without --dry-run to remove.")
		return
	}
	if !f.yes {
		if !IsTTY(os.Stdin) {
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
	for _, s := range RemoveGlobal(root, RemoveOptions{Agents: agents}) {
		mark := removeMark(s)
		switch mark {
		case "ok":
			removed++
		case "!!":
			failed++
		}
		PrintSetupStatus(ui, mark, s)
	}
	fmt.Printf("\nRemoved kern from %d location(s). Restart your agents to drop the MCP server and hooks.\n", removed)
	fmt.Println("Left untouched: ~/.kern data (index, audit, memory) and any project files `kern setup` wrote inside repos.")
	fmt.Println("Re-install any time: kern setup --global-only --global-rules")
	if failed > 0 {
		fatal("setup --remove: %d step(s) failed — see the errors above", failed)
	}
}
