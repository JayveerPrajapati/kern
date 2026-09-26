package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleFragilityHotspots(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	res, err := s.handleFragilityHotspots(context.Background(), map[string]any{
		"root":    ".",
		"commits": "10",
	})
	if err != nil {
		t.Fatalf("handleFragilityHotspots error: %v", err)
	}

	if !strings.Contains(res, "Causal Defect & Fragility Hotspots") {
		t.Errorf("expected report header in output, got: %s", res)
	}
}
