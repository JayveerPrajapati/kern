package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// brokenGoFixture writes a Go module that fails to compile, so the build
// verification deterministically yields a FAIL verdict.
func brokenGoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module broken\n\ngo 1.20\n",
		"main.go": "package main\n\nvar x = undefinedSymbol\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// TestVerifyFailureDoesNotCompleteTask verifies the reliability guarantee that
// a failed verification can never yield a COMPLETED task: when the verification
// verdict is FAIL, TaskService.Verify must fail the task instead of completing
// it.
func TestVerifyFailureDoesNotCompleteTask(t *testing.T) {
	root := brokenGoFixture(t)
	p := &Platform{root: root, ver: verification.NewEngine(root)}
	ts := NewTaskService(p, nil)

	task, res, err := ts.Verify([]string{"build"})
	if err == nil {
		t.Fatal("expected Verify to fail when the build verification fails")
	}
	if task == nil {
		t.Fatal("expected Verify to return a task")
	}
	if res.Verdict != verification.VerdictFail {
		t.Fatalf("verdict = %q, want FAIL", res.Verdict)
	}
	if task.State == domain.TaskCompleted {
		t.Fatal("task must not be COMPLETED on a failed verification")
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("task state = %q, want FAILED", task.State)
	}
}

// TestRunWorkflowClassifiesTask verifies that RunWorkflow routes the task
// through the specialist pipeline: the intent is classified into a task kind
// and the kind-selected workflow (which preserves the human approval gate) is
// driven to its approval step. The closed-loop stages remain the execution
// mechanism; the workflow engine provides classification, routing and the
// RequiresApproval gate.
func TestRunWorkflowClassifiesTask(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir(), ver: verification.NewEngine(t.TempDir())}, nil)

	// The intent classifies as a documentation task, whose workflow must
	// preserve the human approval gate before the first execution step.
	intent := "write documentation for the public API"
	kind := agents.ClassifyTask(intent, "")
	if kind != agents.TaskKindDocumentation {
		t.Fatalf("ClassifyTask kind = %q, want documentation", kind)
	}
	wf := agents.SelectWorkflow(kind)
	if wf.ID != "documentation" {
		t.Fatalf("SelectWorkflow ID = %q, want documentation", wf.ID)
	}
	gate := false
	for _, s := range wf.Steps {
		if s.Action == "approve" && s.RequiresApproval {
			gate = true
		}
	}
	if !gate {
		t.Fatal("selected workflow must preserve the human approval gate (RequiresApproval approve step)")
	}

	// Register the agents the workflow steps route to so execution reaches the
	// approval gate rather than failing-closed on a missing agent.
	reg := ts.Registry()
	for _, a := range []agent.Agent{
		{Agent: domain.Agent{ID: "planner", Type: "planner", Name: "Planner"}},
		{Agent: domain.Agent{ID: "coder", Type: "coder", Name: "Coder"}},
		{Agent: domain.Agent{ID: "reviewer", Type: "reviewer", Name: "Reviewer"}},
	} {
		if err := reg.Register(a); err != nil {
			t.Fatalf("register %s: %v", a.Type, err)
		}
	}

	task, err := ts.RunWorkflow(intent, func(action string, t *agent.Task) (string, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected RunWorkflow to require approval before executing")
	}
	if task == nil {
		t.Fatal("expected RunWorkflow to return a task")
	}
	// The loop stages are the execution mechanism; the workflow parks at the
	// human approval gate, so the task must be waiting approval, not completed.
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("task state = %q, want WAITING_FOR_APPROVAL (parked at approval gate)", task.State)
	}
}
