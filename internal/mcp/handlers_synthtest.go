package mcp

import (
	"context"

	mcpsynthtest "github.com/JayveerPrajapati/kern/internal/mcp/synthtest"
)

// handleSynthesizeTest automatically scaffolds comprehensive table-driven unit tests
// and edge-case invariants for untested functions and methods.
func (s *Server) handleSynthesizeTest(ctx context.Context, args map[string]any) (string, error) {
	return mcpsynthtest.Synthesize(ctx, mcpsynthtest.Hooks{
		LoadIndex: s.loadIndex,
	}, args)
}
