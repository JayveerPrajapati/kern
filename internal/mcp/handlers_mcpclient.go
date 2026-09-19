package mcp

import (
	"context"

	mcpbridge "github.com/JayveerPrajapati/kern/internal/mcp/bridge"
)

// handleMcpCall implements kern_mcp_call: bridges a tool from an external MCP server.
func (s *Server) handleMcpCall(ctx context.Context, args map[string]any) (string, error) {
	return mcpbridge.CallTool(ctx, args)
}
