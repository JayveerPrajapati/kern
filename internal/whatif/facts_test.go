package whatif

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSimulatePopulatesFactsAndLimitations verifies that a simulate call
// populates the Facts and Limitations fields with deterministic content, and
// that both appear in the JSON output.
func TestSimulatePopulatesFactsAndLimitations(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "helper")
	if id == "" {
		t.Fatal("helper symbol not found in graph")
	}

	imp := Simulate(g, Change{Kind: RemoveSymbol, Target: id})

	if len(imp.Facts) == 0 {
		t.Fatal("expected non-empty Facts from a simulate call")
	}
	if len(imp.Limitations) == 0 {
		t.Fatal("expected non-empty Limitations from a simulate call")
	}

	// Facts should capture deterministic observations about the target.
	joined := strings.Join(imp.Facts, "\n")
	for _, want := range []string{"remove_symbol", id, "risk"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Facts missing %q:\n%s", want, joined)
		}
	}

	// Limitations should call out the pipeline evidence gaps.
	lim := strings.Join(imp.Limitations, "\n")
	if !strings.Contains(lim, "no runtime telemetry evidence") {
		t.Errorf("Limitations should note the runtime evidence gap:\n%s", lim)
	}
	if !strings.Contains(lim, "no historical") {
		t.Errorf("Limitations should note the historical evidence gap:\n%s", lim)
	}

	// Both fields must appear in the serialized JSON.
	b, err := json.Marshal(imp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"facts"`) {
		t.Error("JSON output missing \"facts\" key")
	}
	if !strings.Contains(string(b), `"limitations"`) {
		t.Error("JSON output missing \"limitations\" key")
	}
}

// TestSimulateHighLevelLimitation verifies that a high-level change kind notes
// the twin/context-data limitation explicitly.
func TestSimulateHighLevelLimitation(t *testing.T) {
	g := buildGraph(t)
	id := nodeID(g, "top")
	if id == "" {
		t.Fatal("top symbol not found in graph")
	}
	imp := Simulate(g, Change{Kind: SplitService, Target: id})
	found := false
	for _, l := range imp.Limitations {
		if strings.Contains(l, "twin/context data") {
			found = true
		}
	}
	if !found {
		t.Errorf("SplitService should surface a twin/context limitation, got %v", imp.Limitations)
	}
	if len(imp.Facts) == 0 {
		t.Error("expected Facts for a split-service simulation")
	}
}
