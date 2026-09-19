package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/evidence"
)

// EvidenceProof aliases evidence.EvidenceProof for package compatibility.
type EvidenceProof = evidence.EvidenceProof

// handleEvidenceAnchor validates citations made by LLMs, corrects line drift,
// and issues a deterministic SHA-256 evidence certificate.
func (s *Server) handleEvidenceAnchor(ctx context.Context, args map[string]any) (string, error) {
	return evidence.Anchor(ctx, evidence.Hooks{LoadIndex: s.loadIndex}, args)
}
