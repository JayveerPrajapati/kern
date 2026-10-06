package verification

import (
	"strings"
	"testing"
)

func TestCollectImportsSkipsNestedModules(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":             "module outer\n\ngo 1.20\n",
		"main.go":            "package main\nfunc main() {}\n",
		"fixture/go.mod":     "module inner\n\ngo 1.20\n",
		"fixture/inner.go":   "package inner\nimport \"example.com/inner/dep\"\nvar _ = dep.X\n",
		"fixture/sub/sub.go": "package sub\nimport \"example.com/inner/other\"\nvar _ = other.X\n",
		"plain/plain.go":     "package plain\nimport \"example.com/outer/dep\"\nvar _ = dep.X\n",
	})
	imports := collectImports(dir, "outer")
	joined := strings.Join(imports, " ")
	if strings.Contains(joined, "example.com/inner") {
		t.Errorf("imports of a nested module leaked into the outer module: %v", imports)
	}
	if !strings.Contains(joined, "example.com/outer/dep") {
		t.Errorf("imports of the outer module's own packages were dropped: %v", imports)
	}
}
