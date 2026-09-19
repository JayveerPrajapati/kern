package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/review"
)

func (s *Server) reviewHooks() review.Hooks {
	return review.Hooks{
		LoadIndex:      s.loadIndex,
		ChangedContext: s.changedContext,
	}
}

func (s *Server) handleChanges(ctx context.Context, args map[string]any) (string, error) {
	return review.Changes(ctx, s.reviewHooks(), args)
}

func (s *Server) handleReview(ctx context.Context, args map[string]any) (string, error) {
	return review.Review(ctx, s.reviewHooks(), args)
}

func (s *Server) handleHubs(ctx context.Context, args map[string]any) (string, error) {
	return review.Hubs(ctx, s.reviewHooks(), args)
}

func (s *Server) handleTestGaps(ctx context.Context, args map[string]any) (string, error) {
	return review.TestGaps(ctx, s.reviewHooks(), args)
}
