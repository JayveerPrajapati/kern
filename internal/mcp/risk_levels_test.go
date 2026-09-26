package mcp

import (
	"testing"
)

// validRiskLevels is the closed set of RiskLevel values a tool may carry.
var validRiskLevels = map[string]bool{
	RiskLow:      true,
	RiskMedium:   true,
	RiskHigh:     true,
	RiskCritical: true,
}

// TestAllToolsHaveValidRiskLevel (P0-004) enforces that every registered MCP
// tool carries a RiskLevel, and that the value is one of the four known
// levels. This runs against the registration table directly so a new tool
// added without a risk level fails here.
func TestAllToolsHaveValidRiskLevel(t *testing.T) {
	t.Parallel()
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range tools {
		if tool.RiskLevel == "" {
			t.Errorf("tool %s has no RiskLevel assigned", tool.Name)
			continue
		}
		if !validRiskLevels[tool.RiskLevel] {
			t.Errorf("tool %s has invalid RiskLevel %q (want low|medium|high|critical)", tool.Name, tool.RiskLevel)
		}
	}
}

// TestNamedToolRiskLevels (P0-004) pins the risk classification for the
// tools called out in the backlog item: execution tools are critical,
// security-sensitive tools are high, analysis tools are medium, and
// read-only tools are low.
func TestNamedToolRiskLevels(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		// critical — execution tools
		"kern_exec":    RiskCritical,
		"kern_sandbox": RiskCritical,
		"kern_execute": RiskCritical,
		// high — security-sensitive tools
		"kern_security":    RiskHigh,
		"kern_safe_delete": RiskHigh,
		"kern_rename":      RiskHigh,
		// medium — analysis tools
		"kern_analyze": RiskMedium,
		"kern_plan":    RiskMedium,
		"kern_impact":  RiskMedium,
		"kern_verify":  RiskMedium,
		// medium — contained state mutation (D1 F1 reclassification: these
		// were low but mutate state, so they are never cacheable)
		"kern_register_host_sampler": RiskMedium,
		// low — read-only tools
		"kern_search":       RiskLow,
		"kern_explore":      RiskLow,
		"kern_context":      RiskLow,
		"kern_compact_file": RiskLow,
	}
	got := map[string]string{}
	for _, tool := range tools {
		got[tool.Name] = tool.RiskLevel
	}
	for name, wantLevel := range want {
		gotLevel, ok := got[name]
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}
		if gotLevel != wantLevel {
			t.Errorf("tool %s RiskLevel = %q, want %q", name, gotLevel, wantLevel)
		}
	}
}

// TestRiskLevelAdvertisedInToolsList (P0-004) verifies the risk metadata
// reaches the wire: every advertised tool in the tools/list response carries
// a riskLevel field with a valid value.
func TestRiskLevelAdvertisedInToolsList(t *testing.T) {
	t.Setenv("KERN_MCP_FULL", "1")
	resp := serveOne(t, writeReq("tools/list", 3, ``))
	res := resp["result"].(map[string]any)
	items, ok := res["tools"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("tools/list returned no tools: %v", res["tools"])
	}
	for _, item := range items {
		tool := item.(map[string]any)
		name, _ := tool["name"].(string)
		risk, ok := tool["riskLevel"].(string)
		if !ok {
			t.Errorf("advertised tool %s is missing riskLevel", name)
			continue
		}
		if !validRiskLevels[risk] {
			t.Errorf("advertised tool %s has invalid riskLevel %q", name, risk)
		}
	}
}
