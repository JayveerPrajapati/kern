package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

func (s *Server) handlePlanContext(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		pkt, _, err := p.Analyze(change)
		if err != nil {
			return "", err
		}
		budget := 0
		if v := argString(args, "budget"); v != "" {
			n, err := atoiArg(v, budget)
			if err != nil {
				return "", err
			}
			budget = n
		}
		plan := kernctx.PlanPacket(&pkt, change, budget)
		if argBool(args, "json") {
			out, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return "", err
			}
			return string(out), nil
		}
		return kernctx.RenderPlan(plan), nil

	}
}
