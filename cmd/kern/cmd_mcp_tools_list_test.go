package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
)

// `kern mcp tools` is the discoverability front door for the MCP catalog:
// the default 11-tool advertisement hides the long tail behind kern_meta,
// so the listing must show every tool grouped by category.
func TestMCPToolsListsCatalog(t *testing.T) {
	out := captureStdout(t, func() { runMCPToolsList(nil) })
	if !strings.Contains(out, "kern MCP catalog:") {
		t.Fatalf("missing catalog header, got: %s", firstLines(out))
	}
	if !strings.Contains(out, "kern_meta") {
		t.Fatal("kern_meta missing from listing")
	}
	if !strings.Contains(out, "governance (") {
		t.Fatal("governance category group missing")
	}
	if !strings.Contains(out, "KERN_MCP_FULL=1") {
		t.Fatal("full-surface hint missing")
	}
}

// JSON output is machine-readable: every catalog entry, with the filter
// applied consistently in both formats.
func TestMCPToolsJSON(t *testing.T) {
	out := captureStdout(t, func() { runMCPToolsList([]string{"--json"}) })
	var tools []struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Risk     string `json:"riskLevel"`
	}
	if err := json.Unmarshal([]byte(out), &tools); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(tools) != len(catalog.All) {
		t.Fatalf("expected %d tools, got %d", len(catalog.All), len(tools))
	}
	found := false
	for _, tl := range tools {
		if tl.Name == "kern_meta" && tl.Category == "meta" {
			found = true
		}
	}
	if !found {
		t.Fatal("kern_meta missing from JSON output")
	}
}

// Category filtering works both as a flag and as a bare positional arg, and
// an unknown category fails loudly instead of printing an empty listing.
func TestMCPToolsCategoryFilter(t *testing.T) {
	out := captureStdout(t, func() { runMCPToolsList([]string{"graph"}) })
	if !strings.Contains(out, "graph (") {
		t.Fatalf("graph group missing, got: %s", firstLines(out))
	}
	// A graph tool's description may mention kern_meta, so assert on the
	// tool-name column (2-space indent + padded name), not raw substring.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  kern_meta") {
			t.Fatal("meta tool leaked into graph-filtered listing")
		}
	}

	flagOut := captureStdout(t, func() { runMCPToolsList([]string{"--category", "governance"}) })
	if !strings.Contains(flagOut, "governance (") {
		t.Fatalf("governance group missing, got: %s", firstLines(flagOut))
	}

	expectExit(t, 1, func() { runMCPToolsList([]string{"no-such-category"}) })
}

// The `tools` subcommand intercept happens before server flag parsing:
// `kern mcp tools` must never be mistaken for an --addr value (the old
// behavior tried to listen on tcp/tools).
func TestRunMCPToolsIntercept(t *testing.T) {
	out := captureStdout(t, func() { runMCP([]string{"tools"}) })
	if !strings.Contains(out, "kern MCP catalog:") {
		t.Fatalf("runMCP(tools) did not list the catalog, got: %s", firstLines(out))
	}
}

func firstLines(s string) string {
	lines := strings.SplitN(strings.ReplaceAll(s, "\n", " "), " ", 1)
	if len(lines) == 0 {
		return ""
	}
	if len(lines[0]) > 200 {
		return lines[0][:200]
	}
	return lines[0]
}
