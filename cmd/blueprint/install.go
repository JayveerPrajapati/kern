package main

import (
	"fmt"
	"os"
)

// runInstall dispatches `blueprint install <subcommand>` (hook).
func runInstall(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: blueprint install <hook>\n\nSubcommands:\n  hook   Install a pre-commit hook that runs `blueprint check --staged`")
		return 2
	}
	// -h/--help/help is help, not an unknown subcommand (exit 4).
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Println("Usage: blueprint install hook")
		fmt.Println("  Install a pre-commit hook that runs `blueprint check --staged`")
		return 0
	}
	switch args[0] {
	case "hook":
		return runInstallHook(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "blueprint: unknown install subcommand %q\n", args[0])
		return 4
	}
}
