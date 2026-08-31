package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TestWhatIfIsEvidenceAware verifies the exit gate for what-if: the
// simulation is Task/Artifact/Evidence aware — the task carries the
// impact_report + risk_report artifacts AND the simulation's typed claims
// (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION) as task evidence, not only as
// rendered text.
func TestWhatIfIsEvidenceAware(t *testing.T) {
	root := safeChangeRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, text, err := ts.WhatIf(whatif.RemoveSymbol, "UserService", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Errorf("state = %s, want COMPLETED", task.State)
	}
	if text == "" {
		t.Error("WhatIf returned empty text")
	}
	// Artifact awareness: impact + risk artifacts in the chain.
	arts, err := ts.Artifacts().GetByTask(task.ID)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	kindSet := map[string]bool{}
	for _, a := range arts {
		kindSet[string(a.Kind)] = true
	}
	for _, want := range []string{"impact_report", "risk_report"} {
		if !kindSet[want] {
			t.Errorf("what-if chain missing %q artifact (got %v)", want, keysOf(kindSet))
		}
	}
	// Evidence awareness: typed claims attached to the task.
	if len(task.Evidence) == 0 {
		t.Error("what-if task carries no evidence claims (exit gate)")
	}
	hasTyped := false
	for _, c := range task.Evidence {
		if c.Statement != "" && c.Type != "" {
			hasTyped = true
		}
	}
	if !hasTyped {
		t.Errorf("what-if evidence claims lack typed content: %+v", task.Evidence)
	}
}

// writeTree creates the given files (keys are slash-separated relative paths)
// under a fresh temp dir and returns the dir path. It is a test-only fixture
// helper.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("writeTree: mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writeTree: write %s: %v", path, err)
		}
	}
	return dir
}

// TestModernizeMaterializesPhaseTasks verifies the exit gate for
// modernization: the analysis is Task aware — the plan task is materialized
// AND every extraction phase becomes its own task (Task Group → Tasks), linked
// to the plan task by ParentID and carrying an architecture artifact.
//
// It runs against a small self-contained fixture module (two packages with
// cross-package calls) so community/phase extraction always has real material
// instead of silently no-oping on the current checkout.
func TestModernizeMaterializesPhaseTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (full index build); skipped with -short")
	}
	root := writeTree(t, map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.21\n",
		"pkg/alpha/alpha.go": "package alpha\n\n" +
			"// Greet returns a greeting for name.\n" +
			"func Greet(name string) string { return \"hello, \" + name }\n\n" +
			"// Shout returns an emphatic greeting for name.\n" +
			"func Shout(name string) string { return Greet(name) + \"!\" }\n",
		"pkg/beta/beta.go": "package beta\n\n" +
			"import \"example.com/fixture/pkg/alpha\"\n\n" +
			"// Hello greets name through the alpha package.\n" +
			"func Hello(name string) string { return alpha.Greet(name) }\n\n" +
			"// Hail greets name loudly through the alpha package.\n" +
			"func Hail(name string) string { return alpha.Shout(name) }\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build(fixture): %v", err)
	}
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	planTask, plan, text, err := ts.Modernize()
	if err != nil {
		t.Fatalf("Modernize: %v", err)
	}
	if string(planTask.State) != "COMPLETED" {
		t.Errorf("plan state = %s, want COMPLETED", planTask.State)
	}
	if text == "" {
		t.Error("Modernize returned empty text")
	}
	if len(plan.Phases) == 0 {
		t.Fatalf("modernization extracted no phases from the fixture (got %d contexts, %d bridges) — phase-task materialization not exercisable", len(plan.Contexts), len(plan.Bridges))
	}

	// Task Group → Tasks: every phase task must exist, be linked to the plan
	// task, and carry an architecture artifact.
	var phaseTasks []*domain.Task
	// Use the registry's task list to find phase tasks by parent.
	_ = phaseTasks
	found := 0
	for _, tk := range ts.Registry().ListTasks() {
		if tk.ParentID == planTask.ID {
			found++
			arts, aerr := ts.Artifacts().GetByTask(tk.ID)
			if aerr != nil {
				t.Fatalf("GetByTask(%s): %v", tk.ID, aerr)
			}
			hasArch := false
			for _, a := range arts {
				if a.Kind == domain.ArtifactArchitectureReport {
					hasArch = true
				}
			}
			if !hasArch {
				t.Errorf("phase task %s missing architecture artifact", tk.ID)
			}
		}
	}
	if found != len(plan.Phases) {
		t.Errorf("materialized %d phase tasks, want %d (plan phases)", found, len(plan.Phases))
	}
}
