package intel

// Slice 5: uncertainty must be pinned, not implied. An empty caller/callee
// list is "no attributed evidence" (the graph cannot see it), never a
// confident "no callers" all-clear. Ambiguous bindings must list their
// alternatives. And the index's bare-FullName caller key quirk must not
// silently drop cross-package call edges.

import (
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestEmptyCallersUseAttributionWording pins the explore renderer: when there
// are no attributed callers but call sites exist under a colliding bare name,
// the report names the missing coverage instead of claiming "no callers".
func TestEmptyCallersUseAttributionWording(t *testing.T) {
	rep := &ExploreReport{
		Symbol:         "Dup",
		Resolved:       "Dup",
		Definition:     index.Symbol{Name: "Dup", Kind: "func", File: "pkg/a.go", Line: 1, Lang: "go"},
		UnboundCallers: 2,
	}
	out := RenderExplore(rep)
	if !strings.Contains(out, "none attributed") {
		t.Fatalf("empty callers must say none attributed, got:\n%s", out)
	}
	if !strings.Contains(out, "declared in more than one file") {
		t.Fatalf("empty callers must explain the unbound call sites, got:\n%s", out)
	}
	if strings.Contains(out, "no callers") {
		t.Fatalf("empty callers must not be a confident all-clear:\n%s", out)
	}
}

// TestGraphZeroCalleesSaysNoneAttributed pins the graph renderer: a symbol
// with no resolvable callees says what is NOT counted, never "calls nothing".
func TestGraphZeroCalleesSaysNoneAttributed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":   "module fx\n\ngo 1.20\n",
		"pkg/p.go": "package pkg\n\nfunc Leaf() int { return 1 }\n\nfunc UsesLeaf() int { return Leaf() }\n",
	})
	ix := buildIndex(t, root)

	out, err := GraphCtxMin(ix, "Leaf", 0, "")
	if err != nil {
		t.Fatalf("GraphCtxMin(Leaf): %v", err)
	}
	if !strings.Contains(out, "none attributed in-graph") {
		t.Fatalf("zero callees must say none attributed in-graph, got:\n%s", out)
	}
	if strings.Contains(out, "calls nothing") {
		t.Fatalf("zero callees must not claim the symbol calls nothing:\n%s", out)
	}

	// UsesLeaf has no attributed callers: the graph must not print a
	// confident "no callers"/"0 callers" assertion.
	callersOut, err := GraphCtxMin(ix, "UsesLeaf", 0, "")
	if err != nil {
		t.Fatalf("GraphCtxMin(UsesLeaf): %v", err)
	}
	if strings.Contains(callersOut, "no callers") || strings.Contains(callersOut, "0 callers") {
		t.Fatalf("an empty caller list must not be a confident all-clear:\n%s", callersOut)
	}
}

// TestAmbiguousEdgeListsAlternatives pins the AMBIGUOUS tier: two same-named
// declarations in one file make a bare call ambiguous, and the edge's target
// node must list BOTH candidate declaration IDs (alternatives), with no
// arbitrary file/line fabricated for it.
func TestAmbiguousEdgeListsAlternatives(t *testing.T) {
	first := index.Symbol{Name: "Shared", Kind: "func", File: "pkg/c.go", Line: 5, Lang: "go"}
	second := index.Symbol{Name: "Shared", Kind: "func", File: "pkg/c.go", Line: 9, Lang: "go"}
	caller := index.Symbol{Name: "caller", Kind: "func", File: "pkg/c.go", Line: 1, Lang: "go"}
	ix := &index.Index{
		Root:    ".",
		Symbols: []index.Symbol{caller, first, second},
		Calls: map[string][]index.CallEdge{
			"caller": {{Target: "Shared", SourceFile: "pkg/c.go", Confidence: index.ConfidenceHigh}},
		},
	}
	g, ok := ix.Neighborhood("caller")
	if !ok {
		t.Fatal("caller did not resolve")
	}
	var ambiguous int
	for _, edge := range g.Edges {
		if edge.Resolution != "ambiguous" {
			continue
		}
		ambiguous++
		var target *index.GraphNode
		for i := range g.Nodes {
			if g.Nodes[i].ID == edge.To {
				target = &g.Nodes[i]
			}
		}
		if target == nil {
			t.Fatalf("ambiguous edge has no target node: %+v", edge)
		}
		if len(target.Candidates) != 2 {
			t.Fatalf("ambiguous target must list both alternatives, got %+v", target)
		}
		if target.File != "" || target.Line != 0 {
			t.Fatalf("ambiguous target must not fabricate a location: %+v", target)
		}
		want := []string{first.DeclarationID(), second.DeclarationID()}
		sort.Strings(want)
		got := append([]string(nil), target.Candidates...)
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("ambiguous alternatives = %v, want %v", got, want)
		}
	}
	if ambiguous != 1 {
		t.Fatalf("ambiguous edge count = %d, want 1 (%+v)", ambiguous, g.Edges)
	}
}

// TestCrossPackageBareNameCallersPreserved pins the index quirk: ix.Calls is
// keyed by the caller's bare FullName, so two same-named functions in
// different packages share a bucket. Both edges must survive resolution (the
// export catalog filters owners by SourceFile) instead of one being dropped.
func TestCrossPackageBareNameCallersPreserved(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":           "module fx\n\ngo 1.20\n",
		"shared/shared.go": "package shared\n\nfunc Shared() int { return 1 }\n",
		"a/a.go":           "package a\n\nimport \"fx/shared\"\n\nfunc handle() int { return shared.Shared() }\n",
		"b/b.go":           "package b\n\nimport \"fx/shared\"\n\nfunc handle() int { return shared.Shared() }\n",
	})
	ix := buildIndex(t, root)
	graph := ix.CallBindings()

	files := map[string]bool{}
	for _, edge := range graph.Edges {
		if !strings.Contains(edge.Reference, "Shared") {
			continue
		}
		files[edge.SourceFile] = true
	}
	if len(files) != 2 {
		t.Fatalf("cross-package bare-name callers were dropped: shared call edges from %d files (%v), want 2\nedges: %+v",
			len(files), files, graph.Edges)
	}
}
