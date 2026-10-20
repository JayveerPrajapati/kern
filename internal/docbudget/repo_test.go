package docbudget

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoDocsWithinBudget runs the committed budget manifest against this
// repository's real documents, so a README/AGENTS.md edit that blows its word
// ceiling fails here and not only in the diff gate.
func TestRepoDocsWithinBudget(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "doc-budgets.json")); err != nil {
		t.Skip("budget manifest not found (not running inside the source tree)")
	}
	violations, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Errorf("%s: %s", v.Path, v.Message)
	}
}
