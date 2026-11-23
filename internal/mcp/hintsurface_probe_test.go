package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestHintAvailabilityFollowsAdvertisedSurface (F-B4, dogfood 2026-11-22
// lane B): the hint engine's availability probe must follow the ADVERTISED
// surface (the filteredTools() list tools/list serves), not the
// KERN_MCP_ALLOW_UNADVERTISED invocability escape — the package TestMain
// sets that escape for the whole binary, so this test clears it explicitly
// to prove the probe does not lean on it. On the default surface kern_why
// is not advertised and the explore hint degrades to its kern_impact-only
// alternative; under KERN_MCP_FULL=1 kern_why is advertised again.
func TestHintAvailabilityFollowsAdvertisedSurface(t *testing.T) {
	clearMCPSurfaceEnv(t)
	s := NewServer(strings.NewReader(""), &strings.Builder{})
	if s.advertisedTool("kern_why") {
		t.Fatal("kern_why must not be advertised on the default surface")
	}
	if !s.advertisedTool("kern_impact") {
		t.Fatal("kern_impact must be advertised on the default surface")
	}
	line := s.hintFor(context.Background(), "kern_explore", map[string]any{"agent_id": "probe"}, "some payload", "")
	if line == "" {
		t.Fatal("the explore success hint must fire on the default surface")
	}
	if strings.Contains(line, "kern_why") {
		t.Fatalf("default-surface hint must not advertise kern_why, got %q", line)
	}
	if !strings.Contains(line, "kern_impact") {
		t.Fatalf("default-surface hint should advertise kern_impact, got %q", line)
	}

	// Full surface: kern_why is advertised and the complete hint returns.
	t.Setenv("KERN_MCP_FULL", "1")
	s2 := NewServer(strings.NewReader(""), &strings.Builder{})
	if !s2.advertisedTool("kern_why") {
		t.Fatal("kern_why must be advertised under KERN_MCP_FULL=1")
	}
	line2 := s2.hintFor(context.Background(), "kern_explore", map[string]any{"agent_id": "probe2"}, "some payload", "")
	if !strings.Contains(line2, "kern_why") {
		t.Fatalf("full-surface hint must advertise kern_why, got %q", line2)
	}
}
