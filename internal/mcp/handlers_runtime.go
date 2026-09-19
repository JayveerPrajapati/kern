package mcp

import (
	"context"

	mcpruntime "github.com/JayveerPrajapati/kern/internal/mcp/runtime"
)

// handleRuntime exposes the production-intelligence layer over MCP.
func (s *Server) handleRuntime(ctx context.Context, args map[string]any) (string, error) {
	return mcpruntime.Runtime(ctx, mcpruntime.Hooks{
		LoadIndex: s.loadIndex,
	}, args)
}
