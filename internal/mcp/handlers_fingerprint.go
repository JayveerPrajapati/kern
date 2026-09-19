package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/governance"
	mcpfingerprint "github.com/JayveerPrajapati/kern/internal/mcp/fingerprint"
)

// AgentFingerprintReport re-exports mcpfingerprint.AgentFingerprintReport for backward compat.
type AgentFingerprintReport = mcpfingerprint.AgentFingerprintReport

// handleAgentFingerprint hashes and analyzes an agent's tool-call sequence from the audit log.
func (s *Server) handleAgentFingerprint(ctx context.Context, args map[string]any) (string, error) {
	return mcpfingerprint.Analyze(ctx, mcpfingerprint.Hooks{
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
	}, args)
}
