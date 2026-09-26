// Package planner owns the plan-context MCP tool body (kern_plan_context)
// as a plain function with a Hooks bundle injected by the mcp adapter.
package planner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// Hooks carries the kernel callbacks PlanContext needs: only the platform
// (analyze pipeline). The service layer is not touched, so no other hooks
// are needed.
type Hooks struct {
	PlatformFor func(ctx context.Context, root string) (*app.Platform, error)
}

// PlanContext builds the deterministic plan for a change (the plan packet
// with the task policy applied), rendered as text or JSON.
func PlanContext(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	p, err := h.PlatformFor(ctx, root)
	if err != nil {
		return "", err
	}
	pkt, _, err := p.Analyze(change)
	if err != nil {
		return "", err
	}
	budget := 0
	if v := mcpargs.ArgString(args, "budget"); v != "" {
		n, err := mcpargs.AtoiArg(v, budget)
		if err != nil {
			return "", err
		}
		budget = n
	}
	plan := kernctx.PlanPacket(&pkt, change, budget)
	if mcpargs.ArgBool(args, "json") {
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return kernctx.RenderPlan(plan), nil
}
