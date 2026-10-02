package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/doc"
)

// The doc family lives in internal/mcp/doc. These adapters are the
// dispatch-table surface; Search injects the kernel hooks.

func (s *Server) handleDoc(ctx context.Context, args map[string]any) (string, error) {
	return doc.Tool(ctx, doc.Hooks{
		LoadIndex:   s.loadIndex,
		NewGovernor: s.newGovernor,
	}, args)
}

func (s *Server) handleCommitmsg(ctx context.Context, args map[string]any) (string, error) {
	return doc.Commitmsg(ctx, args)
}

func (s *Server) handlePrecache(ctx context.Context, args map[string]any) (string, error) {
	return doc.Precache(ctx, args)
}
