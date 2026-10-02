package mcp

import (
	"context"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	mcpagentctl "github.com/JayveerPrajapati/kern/internal/mcp/agentctl"
	"github.com/JayveerPrajapati/kern/internal/mcp/coord"
	mcpfingerprint "github.com/JayveerPrajapati/kern/internal/mcp/fingerprint"
)

// AgentFingerprintReport re-exports mcpfingerprint.AgentFingerprintReport for
// backward compatibility (kern_agent action=fingerprint).
type AgentFingerprintReport = mcpfingerprint.AgentFingerprintReport

func (s *Server) agentctlHooks() mcpagentctl.Hooks {
	return mcpagentctl.Hooks{
		PlatformFor:  s.platformFor,
		CoordHandoff: s.coordHandoff,
		AuditFilter: func(agentID string) []governance.AuditEntry {
			s.auditMu.Lock()
			defer s.auditMu.Unlock()
			if s.audit == nil {
				return nil
			}
			return s.audit.Filter(agentID)
		},
		AuditAll: func() []governance.AuditEntry {
			s.auditMu.Lock()
			defer s.auditMu.Unlock()
			if s.audit == nil {
				return nil
			}
			return s.audit.All()
		},
	}
}

// handleAgent implements kern_agent: the action argument dispatches to the
// message/interrupt/fingerprint/coordination/rbac bodies.
func (s *Server) handleAgent(ctx context.Context, args map[string]any) (string, error) {
	return mcpagentctl.Tool(ctx, s.agentctlHooks(), args)
}

// coordHandoff bridges the coordination leaf's handoff body for the
// agentctl message body (kern_agent action=message).
func (s *Server) coordHandoff(root string, now time.Time, agentID, format string, args map[string]any) (string, error) {
	return coord.Handoff(root, now, agentID, format, args)
}
