package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSharedBareConstructorSkipsRewrite pins finding A6: two packages both
// declare `New()` returning different types; a caller doing
// `n := New(); n.Spin()` records the callee as the constructor stand-in
// "New.Spin" (the constructor lives in another file, so extract-time type
// inference cannot resolve it). Last-write-wins over ix.Symbols order used to
// attribute the shared bare constructor name to an arbitrary package's type
// ("New.Spin" -> "Gadget.Spin" when beta.New was processed last). The
// ctorDup guard marks the name ambiguous and skips the rewrite, so the callee
// stays "New.Spin" — the caller's package or nothing, never an arbitrary
// package's type.
func TestSharedBareConstructorSkipsRewrite(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"alpha/alpha.go": `package alpha

type Widget struct{}

func New() *Widget { return &Widget{} }

func (w *Widget) Spin() {}
`,
		"alpha/use.go": `package alpha

func Run() {
	n := New()
	n.Spin()
}
`,
		"beta/beta.go": `package beta

type Gadget struct{}

func New() *Gadget { return &Gadget{} }

func (g *Gadget) Spin() {}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := []string{}
	for _, callees := range ix.Calls {
		for _, ce := range callees {
			switch ce.Target {
			case "Widget.Spin", "Gadget.Spin":
				rewritten = append(rewritten, ce.Target)
			case "New.Spin":
				return // conservative: shared bare constructor name skipped the rewrite
			}
		}
	}
	t.Fatalf("no New.Spin found; over-attributed rewrites: %v (shared bare constructor must skip the rewrite)", rewritten)
}
