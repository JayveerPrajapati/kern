package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/merge"
)

// handleSemanticDiff returns an AST-level symbol diff instead of raw lines.
func (s *Server) handleSemanticDiff(ctx context.Context, args map[string]any) (string, error) {
	return merge.SemanticDiff(ctx, merge.Hooks{LoadIndex: s.loadIndex}, args)
}
