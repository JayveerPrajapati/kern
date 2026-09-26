// Package orchestrate owns the silent context-pipeline MCP tool body
// (kern_orchestrate) as a plain function with a Hooks bundle injected by
// the mcp adapter.
package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// Hooks carries the kernel callbacks Orchestrate needs: only the platform
// (the silent pipeline itself). The service layer is not touched, so no
// other hooks are needed.
type Hooks struct {
	PlatformFor func(ctx context.Context, root string) (*app.Platform, error)
}

// Orchestrate implements kern_orchestrate: runs the silent context
// pipeline (task classification -> planner -> evidence selection -> budgeting
// -> envelope) over an intent and returns the deterministic result as JSON —
// the plan, envelope identity, token accounting, and an escalation handle.
func Orchestrate(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	intent := mcpargs.ArgString(args, "intent")
	if intent == "" {
		return "", fmt.Errorf("intent is required")
	}
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	budget := 0
	if v := mcpargs.ArgString(args, "budget"); v != "" {
		n, err := mcpargs.AtoiArg(v, budget)
		if err != nil {
			return "", err
		}
		budget = n
	}
	p, err := h.PlatformFor(ctx, root)
	if err != nil {
		return "", err
	}
	res, err := p.Orchestrate(intent, kernctx.OrchestrateOptions{
		Budget: budget,
		Mode:   mcpargs.ArgString(args, "mode"),
		Skill:  mcpargs.ArgString(args, "skill"),
	})
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
