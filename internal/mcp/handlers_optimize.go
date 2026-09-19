package mcp

import (
	"context"

	mcpoptimize "github.com/JayveerPrajapati/kern/internal/mcp/optimize"
)

func (s *Server) handleOptimizePrompt(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Prompt(ctx, args)
}

func (s *Server) handleSwap(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Swap(ctx, args)
}

func (s *Server) handleOptimizeLog(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Log(ctx, args)
}

func (s *Server) handleOptimizeOutput(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Output(ctx, args)
}

func (s *Server) handleStats(ctx context.Context, args map[string]any) (string, error) {
	return renderStats(argString(args, "days"), argString(args, "session"))
}

func (s *Server) handleSemcache(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Semcache(ctx, args)
}

func (s *Server) handleContextBudget(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.ContextBudget(ctx, args)
}

func (s *Server) handleFetchAnchor(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.FetchAnchor(ctx, args)
}
