package mcp

import (
	"context"

	mcpoptimize "github.com/JayveerPrajapati/kern/internal/mcp/optimize"
)

func (s *Server) handleOptimize(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Tool(ctx, args)
}

func (s *Server) handleSwap(ctx context.Context, args map[string]any) (string, error) {
	return mcpoptimize.Swap(ctx, args)
}

func (s *Server) handleStats(ctx context.Context, args map[string]any) (string, error) {
	if argBool(args, "by_tool") {
		return renderStatsByTool(argString(args, "days"), argString(args, "session"))
	}
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
