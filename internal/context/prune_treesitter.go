//go:build treesitter

package context

import (
	"github.com/JayveerPrajapati/kern/internal/index"
)

// TreesitterAvailable reports whether tree-sitter AST pruning is enabled in this build.
func TreesitterAvailable(lang string) bool {
	return index.TreeSitterAvailable(lang)
}
