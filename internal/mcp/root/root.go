// Package root centralizes workspace-root resolution for MCP tool packages.
package root

import (
	"os"
	"path/filepath"
)

// ResolveRoot returns the cleaned absolute workspace root: cwd when root is
// empty, filepath.Abs+Clean otherwise, falling back to the raw input if
// Abs fails.
func ResolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}
