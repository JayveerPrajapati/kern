//go:build notreesitter

package index

import "fmt"

// tsExtract is a stub when tree-sitter is not enabled.
func tsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	return nil, nil, nil, nil, fmt.Errorf("tree-sitter not enabled (build without -tags notreesitter to enable)")
}

// treesitterEnabled reports whether tree-sitter extraction is compiled in.
func treesitterEnabled() bool { return false }

// TreesitterEnabled reports whether the tree-sitter extractor is available in
// this build. The default build includes tree-sitter; -tags notreesitter opts
// out and falls back to regex heuristics.
func TreesitterEnabled() bool { return treesitterEnabled() }

// TreeSitterAvailable always returns false when tree-sitter is not enabled.
func TreeSitterAvailable(lang string) bool {
	return false
}
