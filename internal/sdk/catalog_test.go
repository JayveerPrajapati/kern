package sdk

import (
	"strings"
	"testing"
)

// testCatalogEnv pins the governance environment the in-process passthrough
// reads at server construction: no allowlist, no permissive/no-confine
// escapes, default cwd confinement, and the tool cache + audit chain
// redirected away from the repo tree (the audit log and D1 cache default to
// <cwd>/.kern). Must run before NewCatalog.
func testCatalogEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"KERN_TOOLS", "KERN_MCP_ROOTS", "KERN_MCP_PERMISSIVE", "KERN_MCP_NO_CONFINE",
		"KERN_RBAC_DEFAULT_DENY", "KERN_MCP_FULL", "KERN_MCP_HIGH_LEVEL_ONLY",
		"KERN_MCP_SINGLE_TOOL", "KERN_MCP_PHASE", "KERN_MCP_CATEGORY",
	} {
		t.Setenv(v, "")
	}
	t.Setenv("KERN_MCP_CACHE", "0")
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
}

// TestCatalogCallReadOnlyTool proves the in-process passthrough executes a
// real read-only catalog tool (kern_mask_pii: deterministic, no index) and
// returns its raw output.
func TestCatalogCallReadOnlyTool(t *testing.T) {
	testCatalogEnv(t)
	c := NewCatalog()
	defer c.Close()

	out, err := c.Call("kern_mask_pii", map[string]any{"text": "token=sk-abc123def password=hunter2"})
	if err != nil {
		t.Fatalf("Call(kern_mask_pii): %v", err)
	}
	if out == "" {
		t.Fatal("Call returned empty output")
	}
	if !strings.Contains(out, "masked") {
		t.Errorf("expected the masking summary in output, got %q", out)
	}
}

// TestCatalogCallRefusesOutOfConfinementRoot is the security invariant: the
// SDK passthrough must fail closed exactly like MCP. A call whose root
// argument resolves outside the workspace confinement is refused before any
// handler side effect runs.
func TestCatalogCallRefusesOutOfConfinementRoot(t *testing.T) {
	testCatalogEnv(t)
	c := NewCatalog()
	defer c.Close()

	_, err := c.Call("kern_mask_pii", map[string]any{"text": "x", "root": "/etc"})
	if err == nil {
		t.Fatal("Call with an out-of-confinement root arg must be refused, got nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "denied") && !strings.Contains(msg, "outside") {
		t.Fatalf("expected a confinement refusal, got %q", msg)
	}
}

// TestCatalogCallUnknownTool proves an unknown tool name errors like MCP's
// tools/call (dispatch-table miss), never a silent no-op.
func TestCatalogCallUnknownTool(t *testing.T) {
	testCatalogEnv(t)
	c := NewCatalog()
	defer c.Close()

	_, err := c.Call("kern_no_such_tool", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown-tool error, got %v", err)
	}
}

// TestCatalogDiscovery proves the discovery helpers enumerate the full
// catalog (names + descriptions + schemas) without any running server, and
// that the phase/risk filters match their documented semantics.
func TestCatalogDiscovery(t *testing.T) {
	testCatalogEnv(t)
	c := NewCatalog()
	defer c.Close()

	tools := c.Tools()
	if len(tools) < 100 {
		t.Fatalf("Tools() = %d entries, want the full catalog (>= 100)", len(tools))
	}
	seen := map[string]bool{}
	for _, ti := range tools {
		if ti.Name == "" || ti.Description == "" {
			t.Errorf("tool with empty name/description: %+v", ti)
		}
		seen[ti.Name] = true
	}
	if !seen["kern_mask_pii"] {
		t.Error("catalog missing kern_mask_pii")
	}

	schema, err := c.ToolSchema("kern_mask_pii")
	if err != nil {
		t.Fatalf("ToolSchema(kern_mask_pii): %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["text"] == nil {
		t.Errorf("schema properties missing text arg: %v", schema["properties"])
	}
	if _, err := c.ToolSchema("kern_no_such_tool"); err == nil {
		t.Error("ToolSchema(unknown) must error")
	}

	for _, ti := range c.ToolsForPhase("edit") {
		if ti.Phase != "edit" && ti.Phase != "meta" && ti.Phase != "cross" {
			t.Errorf("ToolsForPhase(edit) returned %s with phase %q", ti.Name, ti.Phase)
		}
	}
	if len(c.ToolsForPhase("")) != len(tools) {
		t.Error("ToolsForPhase(\"\") must return the whole catalog")
	}
	for _, ti := range c.ToolsForRisk("low") {
		if ti.RiskLevel != "low" {
			t.Errorf("ToolsForRisk(low) returned %s with risk %q", ti.Name, ti.RiskLevel)
		}
	}
	if len(c.ToolsForRisk("bogus")) != len(tools) {
		t.Error("ToolsForRisk(bogus) must return the whole catalog")
	}
}
