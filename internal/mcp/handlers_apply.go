package mcp

import (
	"context"

	mcpapply "github.com/JayveerPrajapati/kern/internal/mcp/apply"
)

func (s *Server) handleApply(ctx context.Context, args map[string]any) (string, error) {
	return mcpapply.Apply(ctx, mcpapply.Hooks{
		InvalidateSession: func(root string) {
			if sess := s.sessionFor(root); sess != nil {
				sess.Invalidate()
			}
		},
	}, args)
}
