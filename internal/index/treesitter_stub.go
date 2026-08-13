//go:build !treesitter

package index

import "fmt"

// tsExtract is a stub when tree-sitter is not enabled.
func tsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]string, map[string][]string, *Pkg, error) {
	return nil, nil, nil, nil, fmt.Errorf("tree-sitter not enabled (build with -tags treesitter)")
}

// treesitterEnabled reports whether tree-sitter extraction is compiled in.
func treesitterEnabled() bool { return false }

// TreesitterEnabled reports whether the tree-sitter extractor is available in
// this build; without -tags treesitter extraction falls back to regex.
func TreesitterEnabled() bool { return treesitterEnabled() }

// TreeSitterAvailable always returns false when tree-sitter is not enabled.
func TreeSitterAvailable(lang string) bool {
	return false
}
