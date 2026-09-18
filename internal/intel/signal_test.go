package intel

import (
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
