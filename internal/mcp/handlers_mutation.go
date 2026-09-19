package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/mutation"
)

func (s *Server) handleMutationTest(ctx context.Context, args map[string]any) (string, error) {
	return mutation.Test(ctx, args)
}
