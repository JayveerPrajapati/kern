package mcp

import (
	"context"

	mcpagentctl "github.com/JayveerPrajapati/kern/internal/mcp/agentctl"
)

// handleLLMProviders implements kern_llm_providers: which agents kern is attached to.
func (s *Server) handleLLMProviders(ctx context.Context, args map[string]any) (string, error) {
	return mcpagentctl.LLMProviders(ctx, args)
}
