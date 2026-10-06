package mcpserve

import (
	"strings"
	"testing"
)

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

// TestCallOutputBudgetPerToolTable pins the R7/A4 resolution order: per-call
// max_output > per-tool table (capped at an EXPLICIT KERN_MCP_MAX_OUTPUT) >
// global default (env or built-in). A listed tool gets its per-tool default
// when the env is unset; an unlisted tool gets the global cap.
func TestCallOutputBudgetPerToolTable(t *testing.T) {
	// Unset the env so the assertions resolve against the built-in default.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "")
	// Listed tools get their per-tool defaults.
	if b, err := CallOutputBudget("kern_search", map[string]any{}); err != nil || b != 8<<10 {
		t.Fatalf("kern_search = %d, err=%v; want %d", b, err, 8<<10)
	}
	if b, err := CallOutputBudget("kern_compact_file", map[string]any{}); err != nil || b != 48<<10 {
		t.Fatalf("kern_compact_file = %d, err=%v; want %d", b, err, 48<<10)
	}
	// Unlisted tools get the global default — exactly today's behavior.
	if b, err := CallOutputBudget("kern_buddy", map[string]any{}); err != nil || b != defaultOutputBudget {
		t.Fatalf("unlisted tool = %d, err=%v; want %d", b, err, defaultOutputBudget)
	}
	// Per-call max_output beats the table; max_output=0 still disables.
	if b, err := CallOutputBudget("kern_search", map[string]any{"max_output": "100"}); err != nil || b != 100 {
		t.Fatalf("max_output must beat the table, got %d, err=%v", b, err)
	}
	if b, err := CallOutputBudget("kern_search", map[string]any{"max_output": "0"}); err != nil || b != 0 {
		t.Fatalf("max_output=0 must disable, got %d, err=%v", b, err)
	}
	// A4: an EXPLICIT KERN_MCP_MAX_OUTPUT is an UPPER BOUND — a table tool
	// resolves to min(per-tool table, env), so an operator's hard ceiling is
	// never silently overridden by the table; the env remains the last-resort
	// cap for tools with no entry.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "999")
	if b, err := CallOutputBudget("kern_search", map[string]any{}); err != nil || b != 999 {
		t.Fatalf("explicit env cap must cap the per-tool table (min), got %d, err=%v", b, err)
	}
	if b, err := CallOutputBudget("kern_buddy", map[string]any{}); err != nil || b != 999 {
		t.Fatalf("unlisted tool must get the env cap, got %d, err=%v", b, err)
	}
	// A non-binding env (above the table) leaves the table value in charge.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "1048576")
	if b, err := CallOutputBudget("kern_search", map[string]any{}); err != nil || b != 8<<10 {
		t.Fatalf("a non-binding env must leave the table value, got %d, err=%v", b, err)
	}
	// Per-call max_output at/below an explicit env cap still wins — the
	// clamp only bites above the operator's ceiling.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "1048576")
	if b, err := CallOutputBudget("kern_search", map[string]any{"max_output": "5000"}); err != nil || b != 5000 {
		t.Fatalf("per-call max_output at/below the env cap must win, got %d, err=%v", b, err)
	}
	// ADV-2: a per-call max_output ABOVE an explicitly-set KERN_MCP_MAX_OUTPUT
	// clamps to the operator's ceiling — effective budget is min(call, env) —
	// so the env stays a hard cap even when the agent asks for more.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "999")
	if b, err := CallOutputBudget("kern_search", map[string]any{"max_output": "5000"}); err != nil || b != 999 {
		t.Fatalf("per-call max_output above the env cap must clamp to it, got %d, err=%v", b, err)
	}
	// A disabled call (max_output=0) is a per-call value at/below any cap and
	// keeps today's semantics even with the env set.
	if b, err := CallOutputBudget("kern_search", map[string]any{"max_output": "0"}); err != nil || b != 0 {
		t.Fatalf("max_output=0 must stay disabled under the env cap, got %d, err=%v", b, err)
	}
}

// TestSandboxOutputRetainedMarker pins the R7 cursor marker: truncation with
// an anchor advertises slice=<anchor>:lines:A-B|tail:N alongside the existing
// max_output advice; no truncation or no anchor keeps the plain marker.
func TestSandboxOutputRetainedMarker(t *testing.T) {
	big := strings.Repeat("lorem ipsum dolor sit amet ", 2000)
	// Under budget: untouched, no marker.
	if got := SandboxOutputRetained(big, 1<<20, "kern_x", "anchor-0123456789ab"); got != big {
		t.Fatalf("under-budget output was modified")
	}
	// Truncated without an anchor: the plain marker (no slice cursor).
	if got := SandboxOutputRetained(big, 200, "kern_x", ""); strings.Contains(got, "slice=") {
		t.Fatalf("empty anchor must not advertise a cursor: %q", got)
	}
	// Truncated with an anchor: both affordances are advertised.
	got := SandboxOutputRetained(big, 200, "kern_x", "anchor-0123456789ab")
	for _, frag := range []string{"MCP output sandbox", "Pass max_output=N", "slice=anchor-0123456789ab:lines:A-B|tail:N"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("marker missing %q: %q", frag, got)
		}
	}
}
