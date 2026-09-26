package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/blueprint"
)

// Blueprint change-firewall tools bridged into the kern catalog. The handler
// bodies (raw-JSON adaptation, payload decoding, root confinement) live in
// the blueprint leaf; these adapters only forward dispatch args.

func (s *Server) handleValidateStaged(ctx context.Context, args map[string]any) (string, error) {
	return blueprint.ValidateStaged(ctx, args)
}

func (s *Server) handleValidateProposed(ctx context.Context, args map[string]any) (string, error) {
	return blueprint.ValidateProposed(ctx, args)
}

func (s *Server) handleExplainFinding(ctx context.Context, args map[string]any) (string, error) {
	return blueprint.ExplainFinding(ctx, args)
}

func (s *Server) handleRepairGuidance(ctx context.Context, args map[string]any) (string, error) {
	return blueprint.RepairGuidance(ctx, args)
}
