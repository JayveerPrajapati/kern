package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestDeadCodePrivateSymbolCertain(t *testing.T) {
	// inner is private with zero callers: it cannot be reached via interface
	// dispatch from another package, so the verdict is certain.
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Live() {}

func inner() string { return "y" }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	if len(dead) == 0 {
		t.Fatal("expected dead symbols, got none")
	}
	for _, d := range dead {
		if d.Name == "inner" && d.Confidence != ConfidenceCertain {
			t.Fatalf("private dead symbol %s: confidence = %q, want %q",
				d.Name, d.Confidence, ConfidenceCertain)
		}
	}
}

func TestDeadCodeExportedSymbolProbable(t *testing.T) {
	// Public is exported with zero callers: it might be called through an
	// interface invisible to the index, so the verdict is probable.
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Live() {}

func Public() string { return "x" }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	if len(dead) == 0 {
		t.Fatal("expected dead symbols, got none")
	}
	for _, d := range dead {
		if d.Name == "Public" && d.Confidence != ConfidenceProbable {
			t.Fatalf("exported dead symbol %s: confidence = %q, want %q",
				d.Name, d.Confidence, ConfidenceProbable)
		}
	}
}

func TestDeadCodeExportedMethodUncertain(t *testing.T) {
	// Serve is an exported method in a package that declares interfaces; the
	// package could dispatch it through Store, so the verdict is uncertain.
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

type Store interface {
	Serve() string
}

type server struct{}

func (s *server) Serve() string { return "ok" }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	if len(dead) == 0 {
		t.Fatal("expected dead symbols, got none")
	}
	for _, d := range dead {
		if d.Name == "server.Serve" && d.Confidence != ConfidenceUncertain {
			t.Fatalf("exported method %s: confidence = %q, want %q",
				d.Name, d.Confidence, ConfidenceUncertain)
		}
	}
}

func TestRenderDeadSurfacesConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Live() {}

func Public() string { return "x" }

func inner() string { return "y" }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderDead(DeadCode(ix))
	if !strings.Contains(out, "certainly dead") {
		t.Errorf("expected a 'certainly dead' caveat, got:\n%s", out)
	}
	if !strings.Contains(out, "probably dead (may be called via interface dispatch)") {
		t.Errorf("expected an interface-dispatch caveat, got:\n%s", out)
	}
}
