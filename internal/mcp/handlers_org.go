package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/org"
)

// The org family lives in internal/mcp/org. These adapters are the
// dispatch-table surface; all logic is in the leaf.

func (s *Server) handleOrg(ctx context.Context, args map[string]any) (string, error) {
	return org.Tool(ctx, args)
}
