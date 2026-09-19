package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcphealth "github.com/JayveerPrajapati/kern/internal/mcp/health"
)

// handleHealth returns a structured real-time health snapshot of the kern MCP server,
// allowing AI agents to self-diagnose server state, index freshness, cache hit-rate,
// audit chain length, and active operations without guessing or running expensive retries.
func (s *Server) handleHealth(ctx context.Context, args map[string]any) (string, error) {
	auditLen := 0
	s.auditMu.Lock()
	if s.audit != nil {
		auditLen = s.audit.Len()
	}
	s.auditMu.Unlock()

	info := mcphealth.ServerInfo{
		Transport:       s.transport,
		Version:         serverVersion,
		Protocol:        protocolVersion,
		Roots:           s.workspaceRoots(),
		ToolsRegistered: len(tools),
		ToolsAdvertised: len(s.filteredTools()),
		Inflight:        s.Inflight(),
		AuditLength:     auditLen,
		CachedIndex: func(root string) (*index.Index, bool) {
			sess := s.sessionFor(root)
			if sess == nil {
				return nil, false
			}
			return sess.CachedIndex()
		},
	}

	return mcphealth.Health(ctx, info, args)
}
