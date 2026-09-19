package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/lsp"
)

// handleLSPBridge handles the kern_lsp_bridge tool request.
func (s *Server) handleLSPBridge(ctx context.Context, args map[string]any) (string, error) {
	return lsp.Handle(ctx, args)
}
