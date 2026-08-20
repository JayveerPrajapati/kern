package mcp

import (
	"context"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"strings"
)

func (s *Server) handleChanges(ctx context.Context, args map[string]any) (string, error) {
	{
		changes, ix, err := s.changedContext(ctx, args)
		if err != nil {
			return "", err
		}
		return intel.RenderChanges(intel.AnalyzeChangesRanged(ix, changes)), nil

	}
}

func (s *Server) handleReview(ctx context.Context, args map[string]any) (string, error) {
	{
		changes, ix, err := s.changedContext(ctx, args)
		if err != nil {
			return "", err
		}
		maxTokens := 8000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		return intel.ReviewRanged(ix, changes, maxTokens), nil

	}
}

func (s *Server) handleHubs(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var b strings.Builder
		b.WriteString(intel.RenderHubs(intel.Hubs(ix, limit)))
		b.WriteString("\n\n")
		b.WriteString(intel.RenderBridges(intel.Bridges(ix, 15)))
		return b.String(), nil

	}
}

func (s *Server) handleTestGaps(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		c := intel.AnalyzeCoverage(ix)
		c.HotGaps = intel.TestGaps(ix, limit)
		return c.Render(), nil

	}
}
