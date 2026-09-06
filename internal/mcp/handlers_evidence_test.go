package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestHandleEvidenceAnchor(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{kernRepoRoot}

	// Test 1: Verify known symbol (NewServer)
	resSym, err := s.handleEvidenceAnchor(context.Background(), map[string]any{
		"root":   kernRepoRoot,
		"symbol": "NewServer",
	})
	if err != nil {
		t.Fatalf("handleEvidenceAnchor symbol error: %v", err)
	}

	var proof EvidenceProof
	if err := json.Unmarshal([]byte(resSym), &proof); err != nil {
		t.Fatalf("failed to decode proof: %v; raw=%s", err, resSym)
	}
	if !proof.Verified {
		t.Errorf("expected symbol NewServer to be verified, got false")
	}
	if !strings.HasPrefix(proof.EvidenceID, "evidence-sha256:") {
		t.Errorf("unexpected evidence ID format: %s", proof.EvidenceID)
	}
	if proof.Line <= 0 {
		t.Errorf("expected valid line number, got %d", proof.Line)
	}

	// Test 2: Verify file and line claim (claim string)
	resClaim, err := s.handleEvidenceAnchor(context.Background(), map[string]any{
		"root":  kernRepoRoot,
		"claim": "internal/mcp/http.go:20",
	})
	if err != nil {
		t.Fatalf("handleEvidenceAnchor claim error: %v", err)
	}
	var proofClaim EvidenceProof
	if err := json.Unmarshal([]byte(resClaim), &proofClaim); err != nil {
		t.Fatalf("failed to decode claim proof: %v", err)
	}
	if !proofClaim.Verified {
		t.Errorf("expected claim to be verified")
	}
	if proofClaim.Snippet == "" {
		t.Errorf("expected non-empty snippet for verified file:line claim")
	}

	// Test 3: Non-existent symbol
	resMissing, err := s.handleEvidenceAnchor(context.Background(), map[string]any{
		"root":   kernRepoRoot,
		"symbol": "NonExistentFakeFunction_99999",
	})
	if err != nil {
		t.Fatalf("unexpected error for missing symbol: %v", err)
	}
	var proofMissing EvidenceProof
	_ = json.Unmarshal([]byte(resMissing), &proofMissing)
	if proofMissing.Verified {
		t.Errorf("expected verified=false for fake symbol")
	}
}
