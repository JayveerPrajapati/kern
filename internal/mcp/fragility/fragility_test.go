package fragility

import (
	"context"
	"strings"
	"testing"
)

func TestFragilityHotspotsEmptyRepo(t *testing.T) {
	ctx := context.Background()
	// Target on empty / non-existent root should fail or return clean report
	res, err := FragilityHotspots(ctx, map[string]any{
		"root":   t.TempDir(),
		"limit":  "5",
		"format": "markdown",
	})
	if err != nil && !strings.Contains(err.Error(), "fragility") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err == nil && !strings.Contains(res, "Causal Defect & Fragility Hotspots") {
		t.Errorf("expected report header, got: %s", res)
	}
}
