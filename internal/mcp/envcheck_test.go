package mcp

import (
	"strings"
	"testing"
)

// TestValidateMCPSurfaceEnvUnknownCategory pins the I8 fix: a misspelled
// KERN_MCP_CATEGORY (e.g. "search" — not a category; kern_search carries
// CategoryGraph) must fail loudly at startup instead of silently no-oping
// into "no category filtering".
func TestValidateMCPSurfaceEnvUnknownCategory(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_CATEGORY", "search")
	err := ValidateMCPSurfaceEnv()
	if err == nil {
		t.Fatal("ValidateMCPSurfaceEnv(KERN_MCP_CATEGORY=search) must error — search is not a category")
	}
	msg := err.Error()
	if !strings.Contains(msg, "graph") || !strings.Contains(msg, "governance") {
		t.Fatalf("error must list the valid category set, got: %q", msg)
	}
	if !strings.Contains(msg, `KERN_MCP_CATEGORY="search"`) {
		t.Fatalf("error must echo the offending value, got: %q", msg)
	}
}

// TestValidateMCPSurfaceEnvValidCategoryAndEmpty pins the pass cases: a real
// category validates clean, and an unset variable is not an error.
func TestValidateMCPSurfaceEnvValidCategoryAndEmpty(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_CATEGORY", "graph")
	if err := ValidateMCPSurfaceEnv(); err != nil {
		t.Fatalf("category=graph must validate: %v", err)
	}
	t.Setenv("KERN_MCP_CATEGORY", "")
	if err := ValidateMCPSurfaceEnv(); err != nil {
		t.Fatalf("empty category must validate: %v", err)
	}
}

// TestHighLevelToolCountIs36 pins the I8 truth: KERN_MCP_HIGH_LEVEL_ONLY=1
// advertises exactly 36 tools (docs said 38 — stale literal; the code is the
// source of truth). Any intentional change to the set updates this number.
func TestHighLevelToolCountIs36(t *testing.T) {
	if got := len(highLevelTools); got != 36 {
		t.Fatalf("highLevelTools has %d entries, want 36 (docs said 38 — stale)", got)
	}
}
