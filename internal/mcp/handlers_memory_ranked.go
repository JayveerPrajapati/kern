package mcp

import (
	"context"

	mcpmemory "github.com/JayveerPrajapati/kern/internal/mcp/memory"
)

func (s *Server) handleMemoryRanked(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.Ranked(ctx, args)
}
