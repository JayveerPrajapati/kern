package mcp

import (
	"context"

	mcpprompt "github.com/JayveerPrajapati/kern/internal/mcp/prompt"
)

// handlePromptFill dynamically renders standardized, token-efficient agent prompts
// with auto-injected context (project map, compact file, relevant memory lessons).
func (s *Server) handlePromptFill(ctx context.Context, args map[string]any) (string, error) {
	return mcpprompt.Fill(ctx, args)
}
