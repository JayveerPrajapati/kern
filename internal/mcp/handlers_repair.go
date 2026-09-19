package mcp

import (
	"context"

	mcprepair "github.com/JayveerPrajapati/kern/internal/mcp/repair"
)

func (s *Server) handleRepairDiagnostics(ctx context.Context, args map[string]any) (string, error) {
	return mcprepair.Repair(ctx, args)
}
