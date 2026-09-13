package main

import (
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

// runOrchestrate implements `kern orchestrate`: runs the silent context
// pipeline (task classification -> planner -> evidence selection -> budgeting
// -> envelope) over an intent and prints the deterministic result as JSON —
// the plan, envelope identity, token accounting, and an escalation handle.
// The command's only output is JSON.
func runOrchestrate(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	intent := f.change
	if intent == "" && len(args) > 0 {
		// Positional fallback: a bare token is the intent, matching the MCP
		// tool's required `intent` argument.
		intent = args[0]
	}
	if intent == "" {
		fatalUsage("usage: kern orchestrate \"<intent>\" [--root ROOT] [--max-tokens N] [--mode fix|review|architecture|incident|explain] [--with-skill kern-safe-change|kern-investigate|kern-incident-triage]")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	p, err := app.New(root)
	if err != nil {
		fatal("Orchestrate: %v", err)
	}
	res, err := p.Orchestrate(intent, kernctx.OrchestrateOptions{
		Budget: f.maxTokens,
		Mode:   f.mode,
		Skill:  f.withSkill,
	})
	if err != nil {
		fatal("Orchestrate: %v", err)
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal("Orchestrate: %v", err)
	}
	fmt.Println(string(out))
}