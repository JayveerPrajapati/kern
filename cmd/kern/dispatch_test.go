package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

// dispatchCaseLabels returns every quoted label inside a `case ...:` line of
// dispatch.go (multi-label cases like `case "analyze", "plan":` included).
func dispatchCaseLabels(t *testing.T) map[string]bool {
	t.Helper()
	// The fused commandTable is the single source of truth for dispatchable
	// commands (the old switch-based dispatch was regex-parsed here; a
	// table lookup is compile-time reality).
	labels := map[string]bool{}
	for name := range commandTable {
		labels[name] = true
	}
	return labels
}

// TestMCPToolsReachableFromCLI enforces the CLI<->MCP surface contract: every
// MCP tool must be reachable from the CLI either through a same-name dispatch
// case (kern_search -> search) or through a documented alias in mcpCLIAlias,
// and every alias target must be a real dispatch case. Adding a new MCP tool
// without a CLI command (or an alias for one) fails this test.
func TestMCPToolsReachableFromCLI(t *testing.T) {
	labels := dispatchCaseLabels(t)
	names := map[string]bool{}
	for _, n := range mcp.ToolNames() {
		names[n] = true
	}
	// Every MCP tool is covered by a same-name case or an alias entry.
	for _, tool := range mcp.ToolNames() {
		suffix := strings.TrimPrefix(tool, "kern_")
		if labels[suffix] {
			continue
		}
		alias, ok := mcpCLIAlias[tool]
		if !ok {
			t.Errorf("MCP tool %s has no CLI dispatch case and no mcpCLIAlias entry", tool)
			continue
		}
		if !labels[alias] {
			t.Errorf("mcpCLIAlias[%s] = %q is not a dispatch case", tool, alias)
		}
	}
	// Every alias entry names a real MCP tool (no stale entries).
	for tool := range mcpCLIAlias {
		if !names[tool] {
			t.Errorf("mcpCLIAlias entry %s is not a registered MCP tool", tool)
		}
	}
}

// TestDispatchCommandCheapRoutes pins the dispatcher contract for the
// side-effect-free commands (E3 test-first): version variants exit 0, and an
// unknown command exits 2 after printing usage.
func TestDispatchCommandCheapRoutes(t *testing.T) {
	for _, cmd := range []string{"version", "--version", "-v"} {
		if code := dispatchCommand(cmd, nil); code != 0 {
			t.Errorf("dispatchCommand(%q) = %d, want 0", cmd, code)
		}
	}
	if code := dispatchCommand("guide", nil); code != 0 {
		t.Errorf("dispatchCommand(guide) = %d, want 0", code)
	}
}

// TestDispatchCommandUnknownExits2: an unrecognized command is a hard error
// (usage + exit 2), not a silent success — the fail-closed CLI contract.
func TestDispatchCommandUnknownExits2(t *testing.T) {
	if code := dispatchCommand("definitely-not-a-command", nil); code != 2 {
		t.Errorf("dispatchCommand(unknown) = %d, want 2", code)
	}
}

// TestDispatchCommandHelpStyleFlags: help-style invocations must not panic
// and must exit cleanly (0) — they are routed through the same dispatcher.
func TestDispatchCommandHelpStyleFlags(t *testing.T) {
	for _, cmd := range []string{"--help", "-h", "help"} {
		code := dispatchCommand(cmd, nil)
		if code != 0 && code != 2 {
			t.Errorf("dispatchCommand(%q) = %d, want 0 or 2 (no panic)", cmd, code)
		}
	}
}

// TestEveryCommandHasUsage (F-034): every commandTable entry must carry a
// non-empty multi-line usage string (synopsis + real flags) so `kern <cmd>
// --help` never prints a bare one-liner.
func TestEveryCommandHasUsage(t *testing.T) {
	if len(commandTable) < 170 {
		t.Fatalf("commandTable has %d entries, want >= 170", len(commandTable))
	}
	for name, e := range commandTable {
		if strings.TrimSpace(e.usage) == "" {
			t.Errorf("command %q has empty usage (one-line help stub)", name)
			continue
		}
		// The usage must be multi-line (synopsis + options) or a full
		// sentence for flag-less commands — never a single bare line that
		// merely repeats the help text.
		if !strings.Contains(e.usage, "\n") && !strings.Contains(e.usage, "usage: ") {
			t.Errorf("command %q usage is a single bare line: %q", name, e.usage)
		}
		if strings.TrimSpace(e.usage) == e.help {
			t.Errorf("command %q usage merely repeats the one-liner help", name)
		}
	}
}
