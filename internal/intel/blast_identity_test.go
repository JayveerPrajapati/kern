package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func declarationTraversalFixtures() []struct {
	name, suffix, core, caller string
} {
	return []struct {
		name, suffix, core, caller string
	}{
		{"go", "go", "package a\nfunc Core() {}\n", "package fixture\nimport \"example.com/mod/a\"\nfunc Shared() { a.Core() }\nfunc Upstream() { Shared() }\n"},
		{"python", "py", "def Core():\n    pass\n", "from a.core import Core\ndef Shared():\n    Core()\ndef Upstream():\n    Shared()\n"},
		{"typescript", "ts", "export function Core() {}\n", "import { Core } from '../a/core';\nfunction Shared() { Core(); }\nfunction Upstream() { Shared(); }\n"},
	}
}

func TestBlastRadiusDistinctDeclarations(t *testing.T) {
	for _, fixture := range declarationTraversalFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"go.mod":                     "module example.com/mod\n\ngo 1.22\n",
				"a/core." + fixture.suffix:   fixture.core,
				"b/caller." + fixture.suffix: fixture.caller,
				"c/caller." + fixture.suffix: fixture.caller,
				"d/caller." + fixture.suffix: strings.NewReplacer("a.Core()", "_ = a.Core", "    Core()", "    pass", "{ Core(); }", "{}").Replace(fixture.caller),
			})
			ix := buildIndex(t, root)
			rep, err := Explore(ix, "Core", 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"b.Shared", "c.Shared", "b.Upstream", "c.Upstream"} {
				if !contains(rep.BlastRadius, want) {
					t.Errorf("radius = %v, missing distinct declaration %s", rep.BlastRadius, want)
				}
			}
			seen := map[string]bool{}
			if len(rep.BlastSymbols) != 5 {
				t.Errorf("radius = %v, want exactly five declarations without unrelated d callers", rep.BlastRadius)
			}
			for position, symbol := range rep.BlastSymbols {
				identity := symbol.DeclarationID()
				if seen[identity] {
					t.Errorf("duplicate declaration in radius: %+v", symbol)
				}
				seen[identity] = true
				if rep.NearestDepth[rep.BlastRadius[position]] > 2 {
					t.Errorf("unexpected depth for %+v", symbol)
				}
			}
		})
	}
}

func BenchmarkBlastRadiusDistinctDeclarations(benchmark *testing.B) {
	for _, fixture := range declarationTraversalFixtures() {
		benchmark.Run(fixture.name, func(benchmark *testing.B) {
			root := writeTree(benchmark, map[string]string{
				"go.mod":                     "module example.com/mod\n\ngo 1.22\n",
				"a/core." + fixture.suffix:   fixture.core,
				"b/caller." + fixture.suffix: fixture.caller,
				"c/caller." + fixture.suffix: fixture.caller,
			})
			ix, err := index.Build(root)
			if err != nil {
				benchmark.Fatal(err)
			}
			benchmark.ReportAllocs()
			benchmark.ResetTimer()
			for iteration := 0; iteration < benchmark.N; iteration++ {
				radius, _, _, _ := blastRadiusWalk(ix, []string{"Core"}, false)
				if len(radius) != 5 {
					benchmark.Fatalf("radius = %v, want exactly five declarations", radius)
				}
			}
		})
	}
}

func TestBlastRadiusSameNamedWrapper(t *testing.T) {
	ix := buildIndex(t, sameNameRepo(t))
	rep, err := Explore(ix, "a.Shared", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.Shared", "b.Shared", "TestCallsCore", "TestCallsWrapper"} {
		if !contains(rep.BlastRadius, want) {
			t.Errorf("radius = %v, missing %s", rep.BlastRadius, want)
		}
	}
	if rep.NearestDepth["a.Shared"] != 0 || rep.NearestDepth["b.Shared"] != 1 || rep.NearestDepth["TestCallsWrapper"] != 2 {
		t.Errorf("depths = %v, want core=0, wrapper=1, wrapper caller=2", rep.NearestDepth)
	}
}

func TestBlastRadiusCyclesUseDeclarationIdentity(t *testing.T) {
	ix := &index.Index{
		Symbols:       []index.Symbol{{Name: "Loop", Kind: "func", File: "a/a.go", Line: 1, Lang: "go"}, {Name: "Loop", Kind: "func", File: "b/b.go", Line: 1, Lang: "go"}},
		ImportsByFile: map[string][]index.ImportEdge{"a/a.go": {{Path: "b"}}, "b/b.go": {{Path: "a"}}},
		Calls: map[string][]index.CallEdge{"Loop": {
			{Target: "b.Loop", SourceFile: "a/a.go"},
			{Target: "a.Loop", SourceFile: "b/b.go"},
		}},
		Callers: map[string][]string{"a.Loop": {"Loop"}, "b.Loop": {"Loop"}},
		Pkgs:    map[string]*index.Pkg{"a": {Name: "a", Files: []string{"a/a.go"}}, "b": {Name: "b", Files: []string{"b/b.go"}}},
	}
	radius, symbols, depths, _ := blastRadiusWalk(ix, []string{"a.Loop"}, false)
	if len(radius) != 2 || len(symbols) != 2 || depths["a.Loop"] != 0 || depths["b.Loop"] != 1 {
		t.Errorf("cycle radius = %v, symbols = %v, depths = %v", radius, symbols, depths)
	}
	_, _, depths, _ = blastRadiusWalk(ix, []string{"a.Loop", "b.Loop"}, false)
	if depths["a.Loop"] != 0 || depths["b.Loop"] != 0 {
		t.Errorf("multi-root depths = %v, want both declarations at depth zero", depths)
	}
}

func TestBlastRadiusMixedAmbiguousAndResolvedCallers(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "Core", Kind: "func", File: "core/core.go", Line: 1, Lang: "go"},
			{Name: "Shared", Kind: "func", File: "a/a.go", Line: 1, Params: []string{"int"}, Lang: "go"},
			{Name: "Shared", Kind: "func", File: "a/a.go", Line: 2, Params: []string{"string"}, Lang: "go"},
			{Name: "Shared", Kind: "func", File: "b/b.go", Line: 1, Lang: "go"},
		},
		ImportsByFile: map[string][]index.ImportEdge{"a/a.go": {{Path: "core", Alias: "."}}, "b/b.go": {{Path: "core", Alias: "."}}},
		Calls: map[string][]index.CallEdge{"Shared": {
			{Target: "Core", SourceFile: "a/a.go"},
			{Target: "Core", SourceFile: "b/b.go"},
		}},
		Callers: map[string][]string{"Core": {"Shared"}},
	}
	radius, symbols, _, _ := blastRadiusWalk(ix, []string{"Core"}, false)
	if len(radius) != 3 || !contains(radius, "b.Shared") || !contains(radius, "Shared") {
		t.Fatalf("radius = %v, want core, resolved b.Shared, and an opaque Shared caller", radius)
	}
	for _, symbol := range symbols {
		if symbol.File == "a/a.go" {
			t.Errorf("ambiguous overload was guessed: %+v", symbol)
		}
	}
}

func TestBlastRadiusKeepsUnownedSyntheticEdges(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "Core", Kind: "func", File: "core/core.go", Line: 1},
			{Name: "Known", Kind: "func", File: "caller/caller.go", Line: 1},
		},
		Calls: map[string][]index.CallEdge{"Known": {
			{Target: "external", SourceFile: "caller/caller.go"},
			{Target: "Core", Synth: "dispatch"},
		}},
		Callers: map[string][]string{"Core": {"Known"}},
	}
	radius, _, _, _ := blastRadiusWalk(ix, []string{"Core"}, false)
	if !contains(radius, "Known") {
		t.Errorf("radius = %v, unowned synthetic caller was dropped", radius)
	}
}
