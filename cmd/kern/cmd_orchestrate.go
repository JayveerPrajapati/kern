package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/budget"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// runOrchestrate implements `kern orchestrate`: runs the silent context
// pipeline (task classification -> planner -> evidence selection -> budgeting
// -> envelope) over an intent and prints the deterministic result as JSON —
// the plan, envelope identity, token accounting, and an escalation handle.
// The command's only output is JSON.
//
// --mode extends the context-mode presets (fix|review|architecture|incident|
// explain) with three surface-preset values (surface consolidation T2b):
//   - envelope: reproduce the `kern context-envelope` output (p.Analyze +
//     budget.Fit + kernctx.RenderText -> versioned packet JSON); `kern
//     context-envelope` is now a thin wrapper presetting this mode.
//   - plan: reproduce the `kern explain-context` output (p.Analyze +
//     kernctx.PlanPacket + RenderPlan); `kern explain-context` is now a thin
//     wrapper presetting this mode.
//   - full (default): the full pipeline below (classify -> plan -> evidence ->
//     budget -> envelope), i.e. the pre-T2b behavior.
func runOrchestrate(rest []string) {
	f, args := parseFlagsOrDie(rest)
	intent := f.change
	if intent == "" && f.task != "" {
		// explain-context's wrapper passes --task; accept it as the intent.
		intent = f.task
	}
	if intent == "" && len(args) > 0 {
		// Positional fallback: a bare token is the intent, matching the MCP
		// tool's required `intent` argument.
		intent = args[0]
	}
	if intent == "" {
		fatalUsage("usage: kern orchestrate \"<intent>\" [--root ROOT] [--max-tokens N] [--mode fix|review|architecture|incident|explain|envelope|plan|full] [--with-skill NAME]")
	}
	root := projectRoot(f)
	p, err := app.New(root)
	if err != nil {
		fatal("Orchestrate: %v", err)
	}
	// Surface presets: envelope/plan short-circuit the pipeline and render
	// the canonical outputs of the commands they absorb, so the aliases are
	// byte-identical (never a silently changed render).
	switch f.mode {
	case "envelope":
		runEnvelopeMode(p, intent, f.maxTokens)
		return
	case "plan":
		runPlanMode(p, intent, f.budget, f.json)
		return
	}
	// full (default) and the context-mode presets (fix|review|...) keep the
	// current pipeline behavior; "full" explicitly selects it.
	mode := f.mode
	if mode == "full" {
		mode = ""
	}
	res, err := p.Orchestrate(intent, kernctx.OrchestrateOptions{
		Budget: f.maxTokens,
		Mode:   mode,
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

// runEnvelopeMode reproduces `kern context-envelope` exactly: assemble the
// context packet, stamp the envelope/schema versions, optionally fit the
// render to --max-tokens, and print the versioned packet JSON.
func runEnvelopeMode(p *app.Platform, change string, maxTokens int) {
	pkt, _, err := p.Analyze(change)
	if err != nil {
		fatal("ContextEnvelope: %v", err)
	}
	pkt.EnvelopeVersion = domain.EnvelopeVersionV1
	if pkt.SchemaVersion == "" {
		pkt.SchemaVersion = "1.0.0"
	}
	if maxTokens > 0 && pkt.FittedText == "" {
		pkt.FittedText = budget.Fit(kernctx.RenderText(pkt), maxTokens)
		pkt.TokenCount = tokenize.Count(pkt.FittedText)
	}
	out, err := json.MarshalIndent(pkt, "", "  ")
	if err != nil {
		fatal("ContextEnvelope: %v", err)
	}
	fmt.Println(string(out))
}

// runPlanMode reproduces `kern explain-context` exactly: assemble the packet,
// build the explainable plan (PlanPacket with --budget), and render it as
// text (or JSON with --json).
func runPlanMode(p *app.Platform, change string, budgetN int, asJSON bool) {
	pkt, _, err := p.Analyze(change)
	if err != nil {
		fatal("ExplainContext: %v", err)
	}
	plan := kernctx.PlanPacket(&pkt, change, budgetN)
	if asJSON {
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fatal("ExplainContext: %v", err)
		}
		fmt.Println(string(out))
		return
	}
	out := kernctx.RenderPlan(plan)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	fmt.Print(out)
}
