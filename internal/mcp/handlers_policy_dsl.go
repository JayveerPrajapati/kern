package mcp

import (
	"context"

	mcppolicydsl "github.com/JayveerPrajapati/kern/internal/mcp/policydsl"
)

func (s *Server) handlePolicyDSL(ctx context.Context, args map[string]any) (string, error) {
	return mcppolicydsl.Evaluate(ctx, args)
}
