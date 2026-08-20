package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAGENTSmdWrittenWithoutOpencode verifies the neutrality fix: AGENTS.md
// (the universal instruction file every agent reads) must be written even
// when opencode is NOT in the agents list. Previously wireAgentRules was
// gated behind enabled("opencode"), so `kern setup --agents claude` would
// silently skip AGENTS.md — leaving Claude, Codex, Continue, Windsurf, Zed,
// Qwen, Qoder, and Kiro without kern-first rules they all read natively.
func TestAGENTSmdWrittenWithoutOpencode(t *testing.T) {
	dir := t.TempDir()
	// Wire ONLY claude — opencode intentionally absent.
	Wire(dir, []string{"claude"}, false)
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not written when opencode absent: %v", err)
	}
}

// TestAGENTSmdWrittenWithOnlyMCP verifies AGENTS.md is written even for a
// bare `kern setup --agents mcp` run with no agent-specific wiring.
func TestAGENTSmdWrittenWithOnlyMCP(t *testing.T) {
	dir := t.TempDir()
	Wire(dir, []string{"mcp"}, false)
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not written with only mcp: %v", err)
	}
}
