package whatif

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// methodFixture: a type with a method (the interface-dispatch surface) plus a
// plain function, so the B7 limitation can be pinned per target kind.
func methodFixture(t *testing.T) *intel.Graph {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module meth\n\ngo 1.23\n",
		"m/m.go": "package m\n\ntype T struct{}\n\nfunc (t T) Serve() {}\n\nfunc Standalone() {}\n",
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
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := intel.FromIndex(ix)
	return &g
}

// TestSimulateWarnsMethodTargetsOfInterfaceDispatch pins B7: impact for a
// METHOD target must carry the interface-dispatch limitation (callers through
// an interface are invisible to the call graph, so the affected set may
// under-count), while a plain function target carries no such line.
func TestSimulateWarnsMethodTargetsOfInterfaceDispatch(t *testing.T) {
	g := methodFixture(t)

	imp := Simulate(g, Change{Kind: RemoveSymbol, Target: "T.Serve"})
	found := false
	for _, l := range imp.Limitations {
		if l == "interface dispatch is not modelled: callers reaching this method through an interface are invisible to the call graph, so the affected set may under-count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("method target must carry the interface-dispatch limitation, got %v", imp.Limitations)
	}

	imp = Simulate(g, Change{Kind: RemoveSymbol, Target: "Standalone"})
	for _, l := range imp.Limitations {
		if l == "interface dispatch is not modelled: callers reaching this method through an interface are invisible to the call graph, so the affected set may under-count" {
			t.Fatalf("function target must not carry the method-only limitation: %q", l)
		}
	}
}
