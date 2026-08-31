//go:build treesitter

package doctor

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestCheckPrecision_TreeSitterBuild verifies the tree-sitter build reports
// AST-or-better precision for every indexed language (Go stays resolved, all
// others move to ast), so checkPrecision emits an ok finding instead of the
// default-build heuristic warning.
func TestCheckPrecision_TreeSitterBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeGoFile(t, root, "a.go")
	writeFixtureFile(t, root, "app.ts", "export function handle(): void { helper(); }\nfunction helper(): void {}\n")
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f := checkPrecision(root)
	if f.Level != "ok" {
		t.Fatalf("tree-sitter precision = %s, want ok: %+v", f.Level, f)
	}
	if !strings.Contains(f.Detail, "AST-or-better precision") || !strings.Contains(f.Detail, "tree-sitter build") {
		t.Fatalf("detail missing tree-sitter claim: %q", f.Detail)
	}
}
