package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/transform"
)

// handleAstTransform executes an AST-level semantic mutation without fragile regex diffs.
func (s *Server) handleAstTransform(ctx context.Context, args map[string]any) (string, error) {
	return transform.Transform(ctx, transform.Hooks{LoadIndex: s.loadIndex}, args)
}
