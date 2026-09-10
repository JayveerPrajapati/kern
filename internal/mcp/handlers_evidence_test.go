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
	defer s.Close() // drain sessions (watcher + background index saves) before t.TempDir cleanup
	root := fixtureRoot(t)
	s.roots = []string{root}

	// Test 1: Verify known symbol (NewServer)
	resSym, err := s.handleEvidenceAnchor(context.Background(), map[string]any{
		"root":   root,
		"symbol": "NewServer",
		"format": "json",
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
		"root":   root,
		"claim":  "web/handler.go:9",
		"format": "json",
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
		"root":   root,
		"symbol": "NonExistentFakeFunction_99999",
		"format": "json",
	})
	if err != nil {
		t.Fatalf("unexpected error for missing symbol: %v", err)
	}
	var proofMissing EvidenceProof
	_ = json.Unmarshal([]byte(resMissing), &proofMissing)
	if proofMissing.Verified {
		t.Errorf("expected verified=false for fake symbol")
	}

	// D4: compact text is the default and carries the key facts.
	resCompact, err := s.handleEvidenceAnchor(context.Background(), map[string]any{
		"root":   root,
		"symbol": "NewServer",
	})
	if err != nil {
		t.Fatalf("handleEvidenceAnchor compact error: %v", err)
	}
	for _, want := range []string{"evidence: evidence-sha256:", "verified: true", "symbol: NewServer at ", "verification: Symbol NewServer resolved"} {
		if !strings.Contains(resCompact, want) {
			t.Errorf("compact evidence output missing %q in:\n%s", want, resCompact)
		}
	}
	if strings.Contains(resCompact, `"EvidenceID"`) {
		t.Errorf("compact evidence output must not be JSON:\n%s", resCompact)
	}
}
