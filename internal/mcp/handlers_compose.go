package mcp

import (
	"context"

	mcpcompose "github.com/JayveerPrajapati/kern/internal/mcp/compose"
)

// PipelineStep re-exports mcpcompose.PipelineStep for backward compat.
type PipelineStep = mcpcompose.PipelineStep

// handleCompose executes a deterministic pipeline of kern MCP tools in sequence.
func (s *Server) handleCompose(ctx context.Context, args map[string]any) (string, error) {
	return mcpcompose.Compose(ctx, mcpcompose.Hooks{
		// Pipeline steps carry no client progressToken, so token is "" —
		// progress notifications stay suppressed for composed steps.
		RunTool: func(ctx context.Context, id, name string, args map[string]any) (string, error) {
			return s.runTool(ctx, id, "", name, args)
		},
	}, args)
}
