package mcp

import (
	"io"
	"strings"
	"testing"
)

// clearMCPSurfaceEnv blanks every KERN_MCP_* surface switch and KERN_TOOLS so
// each test exercises exactly the env it sets, regardless of the host shell.
func clearMCPSurfaceEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"KERN_MCP_FULL", "KERN_MCP_HIGH_LEVEL_ONLY", "KERN_MCP_SINGLE_TOOL", "KERN_MCP_PHASE", "KERN_TOOLS"} {
		t.Setenv(v, "")
	}
}

// TestFilteredToolsDefaultMinimal verifies the NEW default: no env override
// advertises exactly the minimal 11-tool defaultTools surface.
func TestFilteredToolsDefaultMinimal(t *testing.T) {
	clearMCPSurfaceEnv(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	if len(got) != len(defaultTools) {
		t.Fatalf("default filteredTools() = %d tools, want %d: %v", len(got), len(defaultTools), toolNamesOf(got))
	}
	names := make(map[string]bool, len(got))
	for _, tool := range got {
		names[tool.Name] = true
	}
	for want := range defaultTools {
		if !names[want] {
			t.Errorf("default surface missing %q", want)
		}
	}
	for name := range names {
		if !defaultTools[name] {
			t.Errorf("default surface contains unexpected tool %q", name)
		}
	}
}

// TestFilteredToolsFullCatalog verifies KERN_MCP_FULL=1 opts back in to the
// full 84-tool catalog.
func TestFilteredToolsFullCatalog(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_FULL", "1")
	s := NewServer(strings.NewReader(""), io.Discard)
	if got := len(s.filteredTools()); got != len(tools) {
		t.Fatalf("KERN_MCP_FULL filteredTools() = %d tools, want %d", got, len(tools))
	}
}

// TestFilteredToolsSingleTool verifies KERN_MCP_SINGLE_TOOL=1 still collapses
// the surface to kern_meta alone (highest priority).
func TestFilteredToolsSingleTool(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_SINGLE_TOOL", "1")
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	if len(got) != 1 || got[0].Name != "kern_meta" {
		t.Fatalf("KERN_MCP_SINGLE_TOOL filteredTools() = %v, want [kern_meta]", toolNamesOf(got))
	}
}

// TestFilteredToolsHighLevelOnly verifies the legacy KERN_MCP_HIGH_LEVEL_ONLY
// mode still yields the mid-size highLevelTools set (backward compat).
func TestFilteredToolsHighLevelOnly(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_HIGH_LEVEL_ONLY", "1")
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	if len(got) != len(highLevelTools) {
		t.Fatalf("KERN_MCP_HIGH_LEVEL_ONLY filteredTools() = %d tools, want %d: %v", len(got), len(highLevelTools), toolNamesOf(got))
	}
	names := make(map[string]bool, len(got))
	for _, tool := range got {
		names[tool.Name] = true
	}
	for want := range highLevelTools {
		if !names[want] {
			t.Errorf("high-level surface missing %q", want)
		}
	}
}

// TestFilteredToolsKernToolsAllowlist verifies the KERN_TOOLS allowlist still
// intersects the default minimal surface.
func TestFilteredToolsKernToolsAllowlist(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_TOOLS", "kern_meta,kern_search")
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	if len(got) != 2 {
		t.Fatalf("default + KERN_TOOLS filteredTools() = %d tools, want 2: %v", len(got), toolNamesOf(got))
	}
	for _, want := range []string{"kern_meta", "kern_search"} {
		if !containsTool(got, want) {
			t.Errorf("filtered list missing %s", want)
		}
	}
}

// TestCallToolResolvesUnadvertisedHandler is the correctness invariant for
// the minimal default surface: kern_meta's NL router must still reach
// sub-tool handlers that are NOT advertised. kern_code_graph is not in
// defaultTools, so with default env the tools/call dispatch must resolve it
// to its real handler (which errors on a missing symbol arg) rather than
// failing with "unknown tool".
func TestCallToolResolvesUnadvertisedHandler(t *testing.T) {
	clearMCPSurfaceEnv(t)
	if defaultTools["kern_code_graph"] {
		t.Fatal("test premise broken: kern_code_graph must not be in the default surface")
	}
	text := mcpToolError(t, "kern_code_graph", map[string]any{})
	if strings.Contains(text, "unknown tool") {
		t.Fatalf("unadvertised tool was rejected as unknown: %q", text)
	}
	if !strings.Contains(text, "symbol is required") {
		t.Fatalf("expected the handler's arg-validation error, got %q", text)
	}
}

// phaseSurfaceFor returns the set of tool names that should be advertised for
// the given phase within a tier surface: every tool tagged with the phase
// plus the always-on meta/cross tools.
func phaseSurfaceFor(tier map[string]bool, phase string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		if !tier[t.Name] {
			continue
		}
		if t.Phase == phase || t.Phase == PhaseMeta || t.Phase == PhaseCross {
			out[t.Name] = true
		}
	}
	return out
}

// assertPhaseSurface runs the core phase-filter invariants: the advertised
// list must equal the phase-tagged tools plus always-on meta/cross within the
// tier, and the spot-checked present/absent tools must hold.
func assertPhaseSurface(t *testing.T, phase string, mustHave, mustNotHave []string) {
	t.Helper()
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	want := phaseSurfaceFor(defaultTools, phase)
	if len(got) != len(want) {
		t.Fatalf("phase=%s filteredTools() = %d tools, want %d: %v", phase, len(got), len(want), toolNamesOf(got))
	}
	gotNames := map[string]bool{}
	for _, tool := range got {
		gotNames[tool.Name] = true
		if !want[tool.Name] {
			t.Errorf("phase=%s surface contains unexpected tool %q (phase=%q)", phase, tool.Name, tool.Phase)
		}
	}
	for name := range want {
		if !gotNames[name] {
			t.Errorf("phase=%s surface missing %q", phase, name)
		}
	}
	for _, name := range mustHave {
		if !gotNames[name] {
			t.Errorf("phase=%s surface missing always-on tool %q", phase, name)
		}
	}
	for _, name := range mustNotHave {
		if gotNames[name] {
			t.Errorf("phase=%s surface should not advertise %q", phase, name)
		}
	}
}

// TestFilteredTools_PhaseExplore verifies KERN_MCP_PHASE=explore advertises
// only explore-phase tools plus the always-on meta/cross tools.
func TestFilteredTools_PhaseExplore(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_PHASE", "explore")
	assertPhaseSurface(t, "explore",
		[]string{"kern_search", "kern_meta", "kern_optimize_prompt", "kern_run"},
		[]string{"kern_plan", "kern_verify", "kern_rename"})
}

// TestFilteredTools_PhasePlan verifies KERN_MCP_PHASE=plan advertises only
// plan-phase tools plus the always-on meta/cross tools.
func TestFilteredTools_PhasePlan(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_PHASE", "plan")
	assertPhaseSurface(t, "plan",
		[]string{"kern_plan", "kern_impact", "kern_meta", "kern_optimize_prompt"},
		[]string{"kern_rename", "kern_verify", "kern_probe"})
}

// TestFilteredTools_PhaseEdit verifies KERN_MCP_PHASE=edit advertises only
// edit-phase tools plus the always-on meta/cross tools.
func TestFilteredTools_PhaseEdit(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_PHASE", "edit")
	assertPhaseSurface(t, "edit",
		[]string{"kern_meta", "kern_run", "kern_optimize_prompt"},
		[]string{"kern_plan", "kern_verify", "kern_probe"})
}

// TestFilteredTools_PhaseVerify verifies KERN_MCP_PHASE=verify advertises only
// verify-phase tools plus the always-on meta/cross tools.
func TestFilteredTools_PhaseVerify(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_PHASE", "verify")
	assertPhaseSurface(t, "verify",
		[]string{"kern_verify", "kern_review", "kern_meta", "kern_optimize_prompt"},
		[]string{"kern_plan", "kern_rename", "kern_probe"})
}

// TestFilteredTools_PhaseUnknown verifies an invalid KERN_MCP_PHASE falls
// back to the full default tier surface (no error, just unfiltered).
func TestFilteredTools_PhaseUnknown(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_PHASE", "bogus")
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	if len(got) != len(defaultTools) {
		t.Fatalf("phase=bogus filteredTools() = %d tools, want all %d defaults: %v", len(got), len(defaultTools), toolNamesOf(got))
	}
	for _, tool := range got {
		if !defaultTools[tool.Name] {
			t.Errorf("phase=bogus surface contains unexpected tool %q", tool.Name)
		}
	}
}

// TestFilteredTools_PhaseIntersectsTier verifies phase filtering intersects
// the tier: KERN_MCP_PHASE=explore + KERN_MCP_HIGH_LEVEL_ONLY=1 keeps only
// explore-phase (plus meta/cross) tools from the high-level set.
func TestFilteredTools_PhaseIntersectsTier(t *testing.T) {
	clearMCPSurfaceEnv(t)
	t.Setenv("KERN_MCP_HIGH_LEVEL_ONLY", "1")
	t.Setenv("KERN_MCP_PHASE", "explore")
	s := NewServer(strings.NewReader(""), io.Discard)
	got := s.filteredTools()
	want := phaseSurfaceFor(highLevelTools, "explore")
	if len(got) != len(want) {
		t.Fatalf("phase=explore + high-level filteredTools() = %d tools, want %d: %v", len(got), len(want), toolNamesOf(got))
	}
	gotNames := map[string]bool{}
	for _, tool := range got {
		gotNames[tool.Name] = true
		if !want[tool.Name] {
			t.Errorf("phase=explore + high-level surface contains unexpected tool %q (phase=%q)", tool.Name, tool.Phase)
		}
	}
	for name := range want {
		if !gotNames[name] {
			t.Errorf("phase=explore + high-level surface missing %q", name)
		}
	}
}
