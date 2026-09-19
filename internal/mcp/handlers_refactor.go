package mcp

import (
	"context"

	mcprefactor "github.com/JayveerPrajapati/kern/internal/mcp/refactor"
)

func (s *Server) handleRefactorTransaction(ctx context.Context, args map[string]any) (string, error) {
	return mcprefactor.Transaction(ctx, mcprefactor.Hooks{
		InvalidateSession: func(root string) {
			if sess := s.sessionFor(root); sess != nil {
				sess.Invalidate()
			}
		},
	}, args)
}
