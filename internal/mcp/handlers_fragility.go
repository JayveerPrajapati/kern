package mcp

import (
	"context"

	mcpfragility "github.com/JayveerPrajapati/kern/internal/mcp/fragility"
)

func (s *Server) handleFragilityHotspots(ctx context.Context, args map[string]any) (string, error) {
	return mcpfragility.FragilityHotspots(ctx, args)
}
