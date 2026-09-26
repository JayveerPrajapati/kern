package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/planner"
)

func (s *Server) handlePlanContext(ctx context.Context, args map[string]any) (string, error) {
	return planner.PlanContext(ctx, planner.Hooks{PlatformFor: s.platformFor}, args)
}
