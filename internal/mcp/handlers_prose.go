package mcp

import (
	"context"
	"fmt"
	"strings"
)

// handleProse implements kern_prose: prose-word → symbol candidate lookup via
// the index's build-time inverted vocab (CG-P1-9). Mirrors handleSearch's
// structure (loadIndex, query required, limit default 20) so agents can skip
// the miss-chain (kern_search miss → kern_ast_search miss): ask "middleware"
// and get the exact symbol names that embed that word.
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
	hits := ix.LookupProse(query, limit)
	if len(hits) == 0 {
		return "no prose matches: " + query, nil
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%s (%d words matched)\n", h.Symbol, h.Matched)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
