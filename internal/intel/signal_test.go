package intel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestLargeFunctionsSkipsBundledAssetsAndGenerated(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "BigPlugin", File: "internal/setup/assets/plugin/kern.ts", Line: 85, End: 2976},
			{Kind: "func", Name: "BigGen", File: "gen/foo.pb.go", Line: 1, End: 500},
			{Kind: "func", Name: "BigReal", File: "app/app.go", Line: 1, End: 300},
		},
		GeneratedFiles: map[string]bool{"gen/foo.pb.go": true},
	}
	large := LargeFunctions(ix, 100)
	if len(large) != 1 || large[0].Name != "BigReal" {
		t.Fatalf("expected only BigReal, got %+v", large)
	}
}

func TestAnalyzeCoverageSkipsFuncMain(t *testing.T) {
	edge := func(target string) []index.CallEdge {
		return []index.CallEdge{{Target: target, Confidence: index.ConfidenceHigh}}
	}
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "main", File: "cmd/app/main.go", Line: 10},
			{Kind: "func", Name: "Helper", File: "lib/lib.go", Line: 20},
			{Kind: "func", Name: "Caller", File: "lib/lib.go", Line: 5},
		},
		Calls: map[string][]index.CallEdge{
			"Caller": append(edge("main"), edge("Helper")...),
		},
		Callers: map[string][]string{
			"main":   {"Caller"},
			"Helper": {"Caller"},
		},
	}
	cov := AnalyzeCoverage(ix)
	for _, g := range cov.HotGaps {
		if g.Symbol == "main" {
			t.Fatalf("func main must not be a hotspot, gaps: %+v", cov.HotGaps)
		}
	}
	hot := map[string]bool{}
	for _, g := range cov.HotGaps {
		hot[g.Symbol] = true
	}
	if !hot["Helper"] {
		t.Errorf("Helper stays a hotspot (control), got %+v", cov.HotGaps)
	}
	if cov.Total != 2 {
		t.Errorf("Total = %d, want 2 (main excluded from coverage universe)", cov.Total)
	}
}

// TestCoverageGapsNamesZeroCallerUncovered is the regression test for the
// "kern testgaps never names the uncovered symbols" fix: uncovered symbols
// with 0 production callers are counted but never named in the report or the
// JSON output. A synthetic index with two zero-caller uncovered symbols must
// name both in Render() and include them in the "gaps" JSON field (never
// null).
func TestCoverageGapsNamesZeroCallerUncovered(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Alpha", File: "a/a.go", Line: 10},
			{Kind: "func", Name: "Beta", File: "b/b.go", Line: 20},
		},
	}
	cov := AnalyzeCoverage(ix)
	if cov.Uncovered != 2 {
		t.Fatalf("Uncovered = %d, want 2", cov.Uncovered)
	}
	if len(cov.HotGaps) != 0 {
		t.Fatalf("HotGaps = %+v, want empty (no callers)", cov.HotGaps)
	}
	if len(cov.Gaps) != 2 {
		t.Fatalf("Gaps = %+v, want both zero-caller symbols named", cov.Gaps)
	}
	out := cov.Render()
	if !strings.Contains(out, "untested symbols:") {
		t.Errorf("Render missing the 'untested symbols:' section, got:\n%s", out)
	}
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Errorf("Render must name the zero-caller uncovered symbols, got:\n%s", out)
	}
	// JSON must carry the full list under "gaps", never null.
	b, err := json.Marshal(cov)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"gaps"`) ||
		!strings.Contains(string(b), "Alpha") ||
		!strings.Contains(string(b), "Beta") {
		t.Errorf("JSON gaps must name the uncovered symbols, got: %s", b)
	}
}
