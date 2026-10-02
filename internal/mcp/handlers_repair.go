package mcp

import (
	"context"

	mcprepair "github.com/JayveerPrajapati/kern/internal/mcp/repair"
)

func (s *Server) handleRepair(ctx context.Context, args map[string]any) (string, error) {
	return mcprepair.Tool(ctx, args)
}
