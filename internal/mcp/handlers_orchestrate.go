package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

// handleOrchestrate implements kern_orchestrate: runs the silent context
// pipeline (task classification -> planner -> evidence selection -> budgeting
// -> envelope) over an intent and returns the deterministic result as JSON —
// the plan, envelope identity, token accounting, and an escalation handle.
func (s *Server) handleOrchestrate(ctx context.Context, args map[string]any) (string, error) {
	intent := argString(args, "intent")
	if intent == "" {
		return "", fmt.Errorf("intent is required")
	}
	root := argString(args, "root")
	if root == "" {
		root = "."
	}
	budget := 0
	if v := argString(args, "budget"); v != "" {
		n, err := atoiArg(v, budget)
		if err != nil {
			return "", err
		}
		budget = n
	}
	p, err := s.platformFor(ctx, root)
	if err != nil {
		return "", err
	}
	res, err := p.Orchestrate(intent, kernctx.OrchestrateOptions{
		Budget: budget,
		Mode:   argString(args, "mode"),
		Skill:  argString(args, "skill"),
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
