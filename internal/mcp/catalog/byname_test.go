package catalog

import "testing"

// TestByName verifies the O(1) lookup that replaced firstQueryParam's linear
// scan: every registered tool resolves, unknown names miss.
func TestByName(t *testing.T) {
	if len(All) == 0 {
		t.Fatal("catalog empty")
	}
	for _, tool := range All {
		got, ok := ByName(tool.Name)
		if !ok {
			t.Fatalf("ByName(%q): not found", tool.Name)
		}
		if got.Name != tool.Name {
			t.Fatalf("ByName(%q) = %q", tool.Name, got.Name)
		}
	}
	if _, ok := ByName("kern_definitely_not_a_tool"); ok {
		t.Fatal("ByName(unknown): found")
	}
}

func TestToolsForPhase(t *testing.T) {
	exploreTools := ToolsForPhase(PhaseExplore)
	if len(exploreTools) == 0 || len(exploreTools) >= len(All) {
		t.Fatalf("exploreTools = %d, expected focused subset (total %d)", len(exploreTools), len(All))
	}
	allTools := ToolsForPhase("")
	if len(allTools) != len(All) {
		t.Fatalf("empty phase tools = %d, want %d", len(allTools), len(All))
	}
}

func TestToolsForRisk(t *testing.T) {
	lowTools := ToolsForRisk(RiskLow)
	if len(lowTools) == 0 || len(lowTools) >= len(All) {
		t.Fatalf("lowTools = %d, expected subset", len(lowTools))
	}
	for _, tool := range lowTools {
		if tool.RiskLevel != RiskLow {
			t.Fatalf("tool %s has risk %q, want low", tool.Name, tool.RiskLevel)
		}
	}
}
