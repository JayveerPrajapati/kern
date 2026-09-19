package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/stream"
)

func (s *Server) handleStream(ctx context.Context, args map[string]any) (string, error) {
	return stream.Handle(ctx, s.transport, args)
}
