package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/envelope"
)

func (s *Server) handleContextEnvelope(ctx context.Context, args map[string]any) (string, error) {
	return envelope.ContextEnvelope(ctx, envelope.Hooks{
		PlatformFor:     s.platformFor,
		LoadIndex:       s.loadIndex,
		FreshnessFooter: s.freshnessFooter,
	}, args)
}
