package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestCostHintForTiers (P2-9): the deterministic cost tiers — slow for
// exec/verify/LLM-class tools, medium for index-backed search/graph,
// fast (default) for everything else.
func TestCostHintForTiers(t *testing.T) {
	if h := costHintFor("kern_sandbox"); h != costSlow {
		t.Errorf("kern_sandbox = %+v, want slow tier", h)
	}
	if h := costHintFor("kern_analyze"); h != costSlow {
		t.Errorf("kern_analyze = %+v, want slow tier (LLM-class)", h)
	}
	if h := costHintFor("kern_search"); h != costMedium {
		t.Errorf("kern_search = %+v, want medium tier", h)
	}
	if h := costHintFor("kern_health"); h != costFast {
		t.Errorf("kern_health = %+v, want fast tier (default)", h)
	}
	// Unknown tools must never panic or mislead: deterministic fast default.
	if h := costHintFor("kern_does_not_exist"); h != costFast {
		t.Errorf("unknown tool = %+v, want fast default", h)
	}
}

// TestHandleMetaCostHintMarker (P2-9): the kern_meta classified line carries
// the est latency + output-token hint so agents can budget context from the
// highest-traffic tool. Exercises the REAL handleMeta path (no mirrors).
func TestHandleMetaCostHintMarker(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	out, err := s.handleMeta(context.Background(), map[string]any{"request": "show me the savings"})
	if err != nil {
		t.Fatalf("handleMeta: %v", err)
	}
	if !strings.Contains(out, "classified as: kern_stats") {
		t.Fatalf("expected kern_stats classification, got: %q", out)
	}
	if !strings.Contains(out, "· est ") || !strings.Contains(out, "ms · ") || !strings.Contains(out, "out tokens") {
		t.Errorf("classified line lacks the cost hint (est Nms · N out tokens): %q", out)
	}
}
