package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/retrieve"
)

// The retrieve family lives in internal/mcp/retrieve. These adapters
// resolve the index and inject the GovContext hooks.

func (s *Server) handleRetrieve(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return retrieve.Retrieve(ctx, ix, s.govContext(ctx, args, ix), args)
}

func (s *Server) handleResolve(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return retrieve.Resolve(ctx, ix, s.govContext(ctx, args, ix), args)
}
