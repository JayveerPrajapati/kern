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

// TestDeadCodeFieldReceiverMethodNotReported pins the field-receiver lens
// fix: a live method invoked through a struct field
// ("a.taskSvc.Deploy" -> "TaskService.Deploy") must never be listed dead.
// Before the fix the callee stayed unresolved, the canonical Callers map was
// empty, and kern dead flagged the method as a false positive.
func TestDeadCodeFieldReceiverMethodNotReported(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app/app.go": `package app

type TaskService struct{}

func (t *TaskService) Deploy() {}
`,
		"app/use.go": `package app

type App struct {
	taskSvc TaskService
}

func Use(a *App) {
	a.taskSvc.Deploy()
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	for _, d := range dead {
		if d.Name == "TaskService.Deploy" {
			t.Fatalf("TaskService.Deploy reported dead, but Use calls it via the taskSvc field (callees: %v)", ix.Calls["Use"])
		}
	}
	// Sanity: the edge really landed in the canonical map (the lens fix, not
	// an empty index, is what kept it out of the dead report).
	if got := ix.Callers["TaskService.Deploy"]; len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[TaskService.Deploy] = %v, want [Use]", got)
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
