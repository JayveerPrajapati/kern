package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/setup"
)

func runSkills(rest []string) {
	if len(rest) == 0 || rest[0] == "list" {
		listSkills()
		return
	}

	sub := rest[0]
	switch sub {
	case "show":
		if len(rest) < 2 {
			fatalUsage("usage: kern skills show <skill-name>")
		}
		showSkill(rest[1])
	case "install":
		installSkills(rest[1:])
	case "check":
		checkSkillsCLI()
	case "help", "-h", "--help":
		printSkillsHelp()
	default:
		for _, name := range setup.SkillNames {
			if name == sub {
				showSkill(name)
				return
			}
		}
		fatalUsage("unknown skills subcommand %q (try: list, show, install, check)", sub)
	}
}

func listSkills() {
	fmt.Println("Bundled Kern Agent Skills:")
	fmt.Println()
	for _, name := range setup.SkillNames {
		data, err := setup.ReadSkill(name)
		if err != nil {
			fmt.Printf("  • %-22s (error reading: %v)\n", name, err)
			continue
		}
		desc := extractSkillDesc(string(data))
		fmt.Printf("  • %-22s %s\n", name, desc)
	}
	fmt.Println()
	fmt.Println("Run 'kern skills show <name>' to view the runbook.")
	fmt.Println("Run 'kern skills install' to deploy to project and global agents.")
}

func showSkill(name string) {
	data, err := setup.ReadSkill(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: unknown skill %q (available: %s)\n", name, strings.Join(setup.SkillNames, ", "))
		panic(exitError{code: 1})
	}
	fmt.Println(string(data))
}

func installSkills(args []string) {
	globalOnly := false
	root := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--global", "-g":
			globalOnly = true
		case "--root", "-r":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		}
	}

	if !globalOnly {
		fmt.Println("Deploying project skills:")
		for _, s := range setup.WireProjectSkills(root) {
			mark := "ok"
			if !s.Installed {
				mark = "!!"
			}
			fmt.Printf("  [%s] %-30s %s\n", mark, s.Agent, s.Note)
		}
	}

	fmt.Println("Deploying global skills across installed agents:")
	for _, s := range setup.WireGlobalSkills() {
		mark := "ok"
		if s.Skipped {
			mark = "--"
		} else if !s.Installed {
			mark = "!!"
		}
		fmt.Printf("  [%s] %-30s %s\n", mark, s.Agent, s.Note)
	}
}

func checkSkillsCLI() {
	statuses := setup.CheckSkills(".")
	fmt.Println("Agent Skills Status:")
	for _, s := range statuses {
		mark := "-"
		if s.Installed {
			mark = "x"
		}
		fmt.Printf("  [%s] %-30s %s\n", mark, s.Agent, s.Note)
	}
}

func extractSkillDesc(content string) string {
	lines := strings.Split(content, "\n")
	inDesc := false
	var desc strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			if val == ">-" || val == "|" || val == ">" || val == "" {
				inDesc = true
				continue
			}
			return strings.Trim(val, "\"'")
		} else if inDesc {
			if strings.HasPrefix(trimmed, "---") || !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				break
			}
			if trimmed != "" {
				if desc.Len() > 0 {
					desc.WriteString(" ")
				}
				desc.WriteString(trimmed)
			}
		}
	}
	if desc.Len() > 0 {
		return desc.String()
	}
	return "Kern agent workflow runbook"
}

func printSkillsHelp() {
	fmt.Println("Usage: kern skills <subcommand> [args]")
	fmt.Println()
	fmt.Println("Manage and inspect agent skills bundled with kern:")
	fmt.Println("  list              List all available skills (default)")
	fmt.Println("  show <name>       Display the complete runbook for a skill")
	fmt.Println("  install [--global] Deploy skills to project and/or global agent directories")
	fmt.Println("  check             Check installation status across agent targets")
}
