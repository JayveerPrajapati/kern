package mcp

import (
	"context"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/coord"
)

// The coordination family lives in internal/mcp/coord. These adapters are the
// dispatch-table surface; all logic and state is in the leaf package.

// AgentHandoff is re-exported from internal/mcp/coord for backward compatibility.
type AgentHandoff = coord.AgentHandoff

// ResourceClaim is re-exported from internal/mcp/coord for backward compatibility.
type ResourceClaim = coord.ResourceClaim

func (s *Server) handleAgentCoordination(ctx context.Context, args map[string]any) (string, error) {
	return coord.Handle(ctx, args)
}

func (s *Server) coordHandoff(root string, now time.Time, agentID, format string, args map[string]any) (string, error) {
	return coord.Handoff(root, now, agentID, format, args)
}
