package mcp

import (
	"context"

	mcpexec "github.com/JayveerPrajapati/kern/internal/mcp/exec"
)

func (s *Server) handleSandbox(ctx context.Context, id string, args map[string]any) (string, error) {
	return mcpexec.Sandbox(ctx, id, args)
}

func (s *Server) handleDiffFiles(ctx context.Context, args map[string]any) (string, error) {
	return mcpexec.DiffFiles(ctx, mcpexec.Hooks{LoadIndex: s.loadIndex}, args)
}

func (s *Server) handleHeal(ctx context.Context, id string, args map[string]any) (string, error) {
	return mcpexec.Heal(ctx, args)
}

func (s *Server) handleValidate(ctx context.Context, args map[string]any) (string, error) {
	return mcpexec.Validate(ctx, args)
}

func (s *Server) handleExec(ctx context.Context, args map[string]any) (string, error) {
	return mcpexec.Exec(ctx, args)
}
