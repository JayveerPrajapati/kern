package index

import "testing"

// TestPackageClausesAreNotSymbols pins B6 (deep-dive 2026-10-03): package /
// module / import clauses must never be indexed as first-class symbols — a
// "package com.foobar;" line becoming a symbol named "foobar" would pollute
// search results, degree counts and hub rankings with organizational noise.
// Verified stale at HEAD for every extractor path (tree-sitter, regex, Go
// AST); this pin keeps it that way.
func TestPackageClausesAreNotSymbols(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		lang string
		src  []byte
	}{
		{"java", "Widget.java", "java",
			[]byte("package com.foobar;\n\npublic class Widget {\n  public void save() {}\n}\n")},
		{"python", "widget.py", "python",
			[]byte("import os\n\n\nclass Widget:\n    def save(self):\n        pass\n")},
	}
	// organizationalNoise: clause-derived names that must never appear as
	// symbols. Widget/save are real definitions and expected.
	noise := map[string]bool{"com.foobar": true, "foobar": true, "os": true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both extractor paths: tree-sitter (default build) and regex
			// (notreesitter build).
			for _, syms := range [][]Symbol{
				mustExtractForeign(t, tc.rel, tc.src, tc.lang),
				mustExtractForeignRegex(t, tc.rel, tc.src, tc.lang),
			} {
				if len(syms) == 0 {
					t.Fatalf("%s: expected extraction to produce symbols", tc.name)
				}
				for _, s := range syms {
					if noise[s.Name] {
						t.Errorf("%s: organizational clause indexed as symbol: kind=%q name=%q", tc.name, s.Kind, s.Name)
					}
				}
			}
		})
	}
	// Go package clauses (goast path).
	syms, _, _, _, err := extract("main.go", []byte("package main\n\nfunc main() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Name != "main" || syms[0].Kind != "func" {
		t.Fatalf("go extraction must yield only func main, got %+v", syms)
	}
}

func mustExtractForeign(t *testing.T, rel string, src []byte, lang string) []Symbol {
	t.Helper()
	syms, _, _, _, err := extractForeign(rel, src, lang)
	if err != nil {
		t.Fatal(err)
	}
	return syms
}

func mustExtractForeignRegex(t *testing.T, rel string, src []byte, lang string) []Symbol {
	t.Helper()
	syms, _, _, _, err := extractForeignRegex(rel, src, lang)
	if err != nil {
		t.Fatal(err)
	}
	return syms
}
