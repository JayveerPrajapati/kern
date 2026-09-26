package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/rbac"
)

// The RBAC family lives in internal/mcp/rbac. These adapters are the
// dispatch-table surface; all logic and state is in the leaf package.
// RoleDefinition is re-exported from internal/governance for backward
// compatibility (the role taxonomy moved there when internal/mcp/rbac hit
// its LOC cap).
type RoleDefinition = governance.RoleDefinition

func (s *Server) handleAgentRoleRBAC(ctx context.Context, args map[string]any) (string, error) {
	return rbac.Handle(ctx, args)
}
