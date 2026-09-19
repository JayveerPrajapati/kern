package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/mcp/prose"
)

// handleProse implements kern_prose: prose-word → symbol candidate lookup via
// the index's build-time inverted vocab.
func (s *Server) handleProse(ctx context.Context, args map[string]any) (string, error) {
	query := argString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	limit := 20
	if v := argString(args, "limit"); v != "" {
		n, err := atoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	return prose.Lookup(ix, query, limit)
}
