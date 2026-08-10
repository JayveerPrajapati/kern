//go:build !treesitter

package index

import "fmt"

// tsExtract is a stub when tree-sitter is not enabled.
func tsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]string, map[string][]string, *Pkg, error) {
	return nil, nil, nil, nil, fmt.Errorf("tree-sitter not enabled (build with -tags treesitter)")
}

// TreeSitterAvailable always returns false when tree-sitter is not enabled.
func TreeSitterAvailable(lang string) bool {
	return false
}
