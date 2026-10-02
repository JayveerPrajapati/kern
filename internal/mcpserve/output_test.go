package mcpserve

import "testing"

// TestOutputBudgetEnvResolution covers the global-cap resolution that lives
// with the budget code (moved out of TestOutputBudgetResolution in the mcp
// root): an env cap applies, and an invalid env falls back to the default.
func TestOutputBudgetEnvResolution(t *testing.T) {
	t.Setenv("KERN_MCP_MAX_OUTPUT", "999")
	if b := outputBudget(); b != 999 {
		t.Fatalf("outputBudget = %d", b)
	}
	// Invalid env falls back to the default.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "junk")
	if b := outputBudget(); b != defaultOutputBudget {
		t.Fatalf("invalid env should fall back to default, got %d", b)
	}
}
