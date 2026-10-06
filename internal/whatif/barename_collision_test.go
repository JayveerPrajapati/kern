package whatif

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// ambiguousFixture: two packages both defining a symbol named Save — the
// bare-name collision case (B4, deep-dive 2026-10-03). Call edges are recorded
// by simple name, so impact for "Save" is the union across both definitions.
func ambiguousFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module amb\n\ngo 1.23\n",
		"a/a.go": "package a\n\nfunc Save() {}\n",
		"b/b.go": "package b\n\nfunc Save() {}\n",
	}
	for rel, src := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestSimulateWarnsOnBareNameCollision pins B4: when the change target's
// simple name has multiple definitions, the impact report must carry a
// limitation saying the counts are the union across all of them (may
// over-count) and pointing at qualified references — instead of presenting a
// merged blast radius as a precise estimate.
func TestSimulateWarnsOnBareNameCollision(t *testing.T) {
	dir := ambiguousFixture(t)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := intel.FromIndex(ix)
	if got := len(g.DefsWithSimpleName("Save")); got < 2 {
		t.Fatalf("fixture must define Save in 2 packages, got %d", got)
	}
	imp := Simulate(&g, Change{Kind: RemoveSymbol, Target: "Save"})
	found := ""
	for _, l := range imp.Limitations {
		if strings.Contains(l, "union across all") && strings.Contains(l, "pkg.Symbol") {
			found = l
			break
		}
	}
	if found == "" {
		t.Fatalf("expected a bare-name collision limitation, got %v", imp.Limitations)
	}
	if !strings.Contains(found, "2 definitions") {
		t.Fatalf("limitation should name both definitions: %q", found)
	}

	// An unambiguous name must NOT warn.
	imp = Simulate(&g, Change{Kind: RemoveSymbol, Target: "a.Save"})
	for _, l := range imp.Limitations {
		if strings.Contains(l, "union across all") {
			t.Fatalf("qualified reference must not carry the collision warning: %q", l)
		}
	}
}
