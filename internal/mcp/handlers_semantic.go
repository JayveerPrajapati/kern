package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/merge"
)

// handleSemantic implements kern_semantic: the action argument dispatches to
// the diff (AST-level symbol diff) or merge (AST-aware 3-way merge) body.
func (s *Server) handleSemantic(ctx context.Context, args map[string]any) (string, error) {
	return merge.Tool(ctx, merge.Hooks{LoadIndex: s.loadIndex}, args)
}
