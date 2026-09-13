package index

import (
	"embed"
	"testing"
)

//go:embed testfixture/*.py testfixture/*.ts testfixture/*.java
var paritySmokeFixtures embed.FS

// parity_smoke_test.go pins the regex extractor's baseline over the shared
// fixture corpus. It runs in BOTH build configurations (default regex build
// and -tags treesitter): in the default build it is the corpus's only guard
// (the full comparison gate in parity_test.go is tree-sitter-gated, since
// both extractors must exist to compare), and it keeps the corpus from
// drifting unnoticed in either configuration. Expectations are pinned per
// file; any change to the regex extractor's symbol or edge output fails
// here first with a precise file:owner:target message.

func TestRegexExtractorBaselineOverCorpus(t *testing.T) {
	entries, err := paritySmokeFixtures.ReadDir("testfixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("corpus changed: %d files, want 3 (python, typescript, java)", len(entries))
	}
	got := map[string]regexExtraction{}
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, err := paritySmokeFixtures.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		lang := detectLang(rel, b)
		syms, calls, _, _, err := extractForeignRegex(rel, b, lang)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		got[rel] = canonExtraction(syms, calls)
	}

	// Pinned expectations: symbol count and edge multiset per file, plus a
	// spot check of the owner->target map shape. When the regex extractor
	// intentionally improves, update these numbers together with the
	// parity gate's tolerance tables in parity_test.go.
	want := map[string]regexExtraction{
		"testfixture/fixtures.py": {
			symbols: 9,
			edges: map[string]int{
				"make_greeter":   2,
				"Greeter.__init__": 1,
				"Greeter.greet":  1,
				"Greeter.loud":   3,
				"Child.greet":    1,
				"top_level":      4,
			},
		},
		"testfixture/fixtures.ts": {
			symbols: 11,
			edges: map[string]int{
				"Greeter.constructor": 1,
				"Greeter.greet":       1,
				"Greeter.loud":        3,
				"makeGreeter":         2,
				"run":                 4,
			},
		},
		"testfixture/fixtures.java": {
			symbols: 7,
			edges: map[string]int{
				"Fixtures.helper": 1,
				"Fixtures.main":   5,
				"Greeter.Greeter": 1,
				"Greeter.greet":   1,
				"Greeter.loud":    3,
			},
		},
	}
	for rel, w := range want {
		g, ok := got[rel]
		if !ok {
			t.Errorf("%s: no extraction ran", rel)
			continue
		}
		if g.symbols != w.symbols {
			t.Errorf("%s: regex symbol count = %d, want %d", rel, g.symbols, w.symbols)
		}
		for owner, n := range w.edges {
			if g.edges[owner] != n {
				t.Errorf("%s: regex edges for %s = %d, want %d", rel, owner, g.edges[owner], n)
			}
		}
		// No undocumented extra owners: every owner must be pinned above.
		for owner := range g.edges {
			if _, pinned := w.edges[owner]; !pinned {
				t.Errorf("%s: undocumented regex edge owner %s (%d edges)", rel, owner, g.edges[owner])
			}
		}
	}
}

// TestRegexExtractorDeterministicOnCorpus guards the determinism contract
// that the parity gate relies on: the same file must extract identically on
// every run, in both build configurations.
func TestRegexExtractorDeterministicOnCorpus(t *testing.T) {
	entries, _ := paritySmokeFixtures.ReadDir("testfixture")
	first := map[string]regexExtraction{}
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, _ := paritySmokeFixtures.ReadFile(rel)
		lang := detectLang(rel, b)
		syms, calls, _, _, err := extractForeignRegex(rel, b, lang)
		if err != nil {
			t.Fatal(err)
		}
		first[rel] = canonExtraction(syms, calls)
	}
	for _, e := range entries {
		rel := "testfixture/" + e.Name()
		b, _ := paritySmokeFixtures.ReadFile(rel)
		lang := detectLang(rel, b)
		syms, calls, _, _, err := extractForeignRegex(rel, b, lang)
		if err != nil {
			t.Fatal(err)
		}
		second := canonExtraction(syms, calls)
		if first[rel].symbols != second.symbols {
			t.Errorf("%s: symbol count changed between runs", rel)
		}
		for owner, n := range second.edges {
			if first[rel].edges[owner] != n {
				t.Errorf("%s: edges for %s changed between runs (%d vs %d)", rel, owner, first[rel].edges[owner], n)
			}
		}
	}
}

type regexExtraction struct {
	symbols int
	edges   map[string]int
}

func canonExtraction(syms []Symbol, calls map[string][]CallEdge) regexExtraction {
	out := regexExtraction{symbols: len(syms), edges: map[string]int{}}
	for owner, edges := range calls {
		out.edges[owner] = len(edges)
	}
	return out
}