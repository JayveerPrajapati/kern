package mcp

import (
	"context"

	mcpskill "github.com/JayveerPrajapati/kern/internal/mcp/skill"
)

// handleSkill implements kern_skill: the model-facing face of the bundled agent-skill catalog.
func (s *Server) handleSkill(ctx context.Context, args map[string]any) (string, error) {
	return mcpskill.Skill(ctx, args)
}
