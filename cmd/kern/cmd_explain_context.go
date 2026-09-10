package main

import (
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

// runExplainContext implements `kern explain-context`: deterministically plans
// which context to include for a change (task type, evidence scoring, budget
// fit) and prints the explainable plan as text or JSON.
func runExplainContext(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	change := f.task
	if change == "" && len(args) > 0 {
		// Positional fallback: a bare token is the change, matching the MCP
		// tool's required `change` argument.
		change = args[0]
	}
	if change == "" {
		fatalUsage("usage: kern explain-context --task \"<change or intent>\" [--root ROOT] [--budget N] [--json]")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	p, err := app.New(root)
	if err != nil {
		fatal("ExplainContext: %v", err)
	}
	pkt, _, err := p.Analyze(change)
	if err != nil {
		fatal("ExplainContext: %v", err)
	}
	plan := kernctx.PlanPacket(&pkt, change, f.budget)
	if f.json {
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fatal("ExplainContext: %v", err)
		}
		fmt.Println(string(out))
		return
	}
	fmt.Print(kernctx.RenderPlan(plan))
}
