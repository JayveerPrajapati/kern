package mcp

import (
	"context"

	mcpagentctl "github.com/JayveerPrajapati/kern/internal/mcp/agentctl"
)

func (s *Server) agentctlHooks() mcpagentctl.Hooks {
	return mcpagentctl.Hooks{
		PlatformFor:  s.platformFor,
		CoordHandoff: s.coordHandoff,
	}
}

// handleAgentMessage implements kern_agent_message: the model sends a
// message to an agent's coordination inbox.
func (s *Server) handleAgentMessage(ctx context.Context, args map[string]any) (string, error) {
	return mcpagentctl.AgentMessage(ctx, s.agentctlHooks(), args)
}

// handleAgentInterrupt implements kern_agent_interrupt: cancels a running
// task by ID through the TaskService.
func (s *Server) handleAgentInterrupt(ctx context.Context, args map[string]any) (string, error) {
	return mcpagentctl.AgentInterrupt(ctx, s.agentctlHooks(), args)
}
