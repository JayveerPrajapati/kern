package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/merge"
)

// handleSemanticMerge executes an AST-aware 3-way merge between base, local,
// and remote versions of source code. Cleanly combines non-overlapping struct
// fields, methods, functions, and imports, and flags precise semantic conflicts.
func (s *Server) handleSemanticMerge(ctx context.Context, args map[string]any) (string, error) {
	return merge.SemanticMerge(ctx, args)
}
