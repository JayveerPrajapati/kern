package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestRenderExploreBalancedIncludesTopContext(t *testing.T) {
	rep := &ExploreReport{
		Symbol:     "ExploreBudgeted",
		Resolved:   "ExploreBudgeted",
		Definition: index.Symbol{Name: "ExploreBudgeted", Kind: "func", File: "internal/intel/explore.go", Line: 168, End: 335},
		Callers:    []string{"Explore", "ExploreMin", "TestExploreBudgetedFitsSource", "TestExploreBudgetedCalleeSkeletons", "runExplore", "ExtraCaller"},
		CallerLocs: map[string]string{
			"Explore":                       "internal/mcp/graph/graph.go:80",
			"ExploreMin":                    "internal/intel/explore.go:104",
			"TestExploreBudgetedFitsSource": "internal/intel/explore_budget_test.go:13",
		},
		Callees:     []string{"ResolveEntry", "AmbiguousAlternatives", "AnchorDefinition", "EdgeConfidenceLabel", "EdgeSynthLabel"},
		BlastRadius: []string{"Explore", "ExploreMin", "runExplore", "ExtraBlast"},
		BlastFiles:  []string{"internal/intel/explore.go", "internal/mcp/graph/graph.go", "internal/intel/explore_budget_test.go", "internal/graphcli/cmd_graph.go", "extra/file.go"},
	}

	out := RenderExploreBalanced(rep)
	for _, want := range []string{
		"# ExploreBudgeted (func) — internal/intel/explore.go:168-335",
		"callers: 6",
		"callees: 5",
		"blast: 4 symbols / 5 files",
		"top callers:",
		"- Explore — internal/mcp/graph/graph.go:80",
		"top callees:",
		"- ResolveEntry",
		"top affected files:",
		"- internal/intel/explore.go",
		"full evidence: mode=evidence",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("balanced output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "== source ==") {
		t.Fatalf("balanced output must not include the full source block:\n%s", out)
	}
}
