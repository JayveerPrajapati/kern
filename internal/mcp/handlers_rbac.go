package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/rbac"
)

// The RBAC family lives in internal/mcp/rbac. These adapters are the
// dispatch-table surface; all logic and state is in the leaf package.

// RoleDefinition is re-exported from internal/mcp/rbac for backward compatibility.
type RoleDefinition = rbac.RoleDefinition

func (s *Server) handleAgentRoleRBAC(ctx context.Context, args map[string]any) (string, error) {
	return rbac.Handle(ctx, args)
}
