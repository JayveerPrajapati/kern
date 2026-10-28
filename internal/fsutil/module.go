package fsutil

import (
	"os"
	"path/filepath"
)

// DiscoverGoModuleRoot returns the Go module directory when root is not
// itself a module but exactly one immediate child is (the common src/ or
// backend/ layout). Ambiguous or absent layouts return "" so callers keep
// root. skipDir, when non-nil, excludes directory names (VCS, vendor,
// generated trees) from the scan.
func DiscoverGoModuleRoot(root string, skipDir func(string) bool) string {
	if HasGoMod(root) {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if skipDir != nil && skipDir(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if HasGoMod(dir) {
			found = append(found, dir)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// HasGoMod reports whether root is a Go module (go.mod present).
func HasGoMod(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}
