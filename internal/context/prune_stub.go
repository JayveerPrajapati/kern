//go:build !treesitter

package context

// TreesitterAvailable reports whether tree-sitter AST pruning is enabled in this build.
func TreesitterAvailable(lang string) bool {
	return false
}
