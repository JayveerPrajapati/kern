package explorebudget

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// buildTree indexes a two-package fixture: lib.Public called by 40 callers in
// their own packages, so the caller list forces the trim path at small
// budgets. Mirrors intel's writeTree/buildIndex helpers.
func buildTree(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lib/lib.go": "package lib\n\n// Public returns the public value.\nfunc Public() {}\n",
	}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("c%d/c%d.go", i, i)] = fmt.Sprintf(
			"package c%d\n\nimport \"lib\"\n\nfunc C%d() { lib.Public() }\n", i, i)
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	return ix
}

func TestRenderEnforcesCap(t *testing.T) {
	ix := buildTree(t)
	rep, err := intel.Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Callers) < 40 {
		t.Fatalf("fixture broken: got %d callers, want >= 40", len(rep.Callers))
	}

	const budget = 120
	out := Render(rep, budget)
	if n := tokenize.Count(out); n > budget {
		t.Errorf("budgeted render = %d tokens, want <= %d\n%s", n, budget, out)
	}
	// Definition core survives.
	if !strings.Contains(out, "symbol:") || !strings.Contains(out, "== source ==") {
		t.Errorf("definition core was dropped:\n%s", out)
	}
	// Trimmed list carries the explicit marker.
	if !strings.Contains(out, "more (raise max_tokens)") {
		t.Errorf("missing '+N more (raise max_tokens)' marker:\n%s", out)
	}

	// A zero/negative budget renders the full report unchanged.
	if full := Render(rep, 0); full != intel.RenderExplore(rep) {
		t.Error("Render(_, 0) must equal intel.RenderExplore")
	}
}

// TestRenderExplainKeepsDocOnce pins that the budgeted explain path keeps the
// item 1 guarantee: the doc comment renders exactly once (source block), and
// the why section references it.
func TestRenderExplainKeepsDocOnce(t *testing.T) {
	ix := buildTree(t)
	rep, err := intel.Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := intel.Why(ix, "Public")
	if !ok {
		t.Fatal("Why did not resolve Public")
	}
	out := RenderExplain(rep, info, 200)
	docFirstLine := "Public returns the public value"
	if got := strings.Count(out, docFirstLine); got != 1 {
		t.Errorf("budgeted explain: doc rendered %d times, want 1:\n%s", got, out)
	}
	if n := tokenize.Count(out); n > 200 {
		t.Errorf("budgeted explain = %d tokens, want <= 200", n)
	}
}
