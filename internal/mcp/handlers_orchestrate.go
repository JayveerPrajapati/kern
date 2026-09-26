package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/orchestrate"
)

func (s *Server) handleOrchestrate(ctx context.Context, args map[string]any) (string, error) {
	return orchestrate.Orchestrate(ctx, orchestrate.Hooks{PlatformFor: s.platformFor}, args)
}
