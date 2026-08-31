package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

// dispatchCaseLabels returns every quoted label inside a `case ...:` line of
// dispatch.go (multi-label cases like `case "analyze", "plan":` included).
func dispatchCaseLabels(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("dispatch.go")
	if err != nil {
		t.Fatalf("read dispatch.go: %v", err)
	}
	labels := map[string]bool{}
	labelRe := regexp.MustCompile(`case\s+(.+):`)
	quoteRe := regexp.MustCompile(`"([a-zA-Z0-9_\-]+)"`)
	for _, line := range strings.Split(string(src), "\n") {
		m := labelRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		for _, q := range quoteRe.FindAllStringSubmatch(m[1], -1) {
			labels[q[1]] = true
		}
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
