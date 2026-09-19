package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// The governor implementation lives in internal/mcp/gov. This file keeps
// the mcp-side surface (thin wrappers) so handler call sites and tests
// compile unchanged; field/method accesses on the governor value use the
// leaf's exported names (gov.PolicySource, gov.FilterGraphText, ...).

const (
	policySourceTaskScope     = provenance.PolicySourceTaskScope
	policySourcePermissive    = provenance.PolicySourcePermissive
	policySourceDefaultScoped = provenance.PolicySourceDefaultScoped
)

// newGovernor runs authorization for a retrieval call. With an explicit
// agent_id it behaves as before (agent + optional task scope). Without one,
// the call is governed by the default agent and a cwd-scoped default scope —
// unless KERN_MCP_PERMISSIVE=1 explicitly restores raw mode (nil governor).
// The firewall is built per call, mirroring kern_authorize_context (the MCP
// server holds no global firewall state). On denial the governor is still
// returned alongside the error so the denial is auditable (its proof carries
// the fingerprint, decided-at and deny policy).
func (s *Server) newGovernor(ctx context.Context, args map[string]any, ix *index.Index) (*gov.Governor, error) {
	return gov.New(ctx, args, ix)
}

func taskScopeFromArgs(args map[string]any, taskID string) *domain.TaskScope {
	return gov.TaskScopeFromArgs(args, taskID)
}
