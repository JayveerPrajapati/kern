package main

import (
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/budget"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// runContextEnvelope implements `kern context-envelope`: assembles the
// context envelope (domain.ContextPacket) for a change and prints it as
// machine-readable JSON with schema versioning. The command's only output is
// JSON, so --json is accepted for symmetry with the other subcommands but has
// no effect.
func runContextEnvelope(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	change := f.change
	if change == "" && len(args) > 0 {
		// Positional fallback: a bare token is the change, matching the MCP
		// tool's required `change` argument.
		change = args[0]
	}
	if change == "" {
		fatalUsage("usage: kern context-envelope --change \"<symbol or change description>\" [--root ROOT] [--max-tokens N]")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	p, err := app.New(root)
	if err != nil {
		fatal("ContextEnvelope: %v", err)
	}
	pkt, _, err := p.Analyze(change)
	if err != nil {
		fatal("ContextEnvelope: %v", err)
	}
	pkt.EnvelopeVersion = domain.EnvelopeVersionV1
	if pkt.SchemaVersion == "" {
		pkt.SchemaVersion = "1.0.0"
	}
	if f.maxTokens > 0 && pkt.FittedText == "" {
		pkt.FittedText = budget.Fit(kernctx.RenderText(pkt), f.maxTokens)
		pkt.TokenCount = tokenize.Count(pkt.FittedText)
	}
	out, err := json.MarshalIndent(pkt, "", "  ")
	if err != nil {
		fatal("ContextEnvelope: %v", err)
	}
	fmt.Println(string(out))
}
