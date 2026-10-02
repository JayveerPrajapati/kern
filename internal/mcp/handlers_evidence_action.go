package mcp

import (
	"context"

	mcpevidence "github.com/JayveerPrajapati/kern/internal/mcp/evidence"
)

// The evidence family lives in internal/mcp/evidence. These adapters are the
// dispatch-table surface; all logic is in the leaf package.

// EvidenceVerifyReport is re-exported from internal/mcp/evidence for backward compatibility.
type EvidenceVerifyReport = mcpevidence.EvidenceVerifyReport

// EvidenceProof aliases evidence.EvidenceProof for package compatibility.
type EvidenceProof = mcpevidence.EvidenceProof

func (s *Server) handleEvidence(ctx context.Context, args map[string]any) (string, error) {
	return mcpevidence.Handle(ctx, mcpevidence.Hooks{
		LoadIndex: s.loadIndex,
	}, args)
}
