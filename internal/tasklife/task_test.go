package tasklife

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verdict"
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
	p := &testPlatform{root: root, ver: verification.NewEngine(root)}
	ts := NewTaskService(p, nil)

	task, res, err := ts.Verify([]string{"build"})
	if err == nil {
		t.Fatal("expected Verify to fail when the build verification fails")
	}
	if task == nil {
		t.Fatal("expected Verify to return a task")
	}
	if res.Verdict != verdict.VerdictFail {
		t.Fatalf("verdict = %q, want FAIL", res.Verdict)
	}
	if task.State == domain.TaskCompleted {
		t.Fatal("task must not be COMPLETED on a failed verification")
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("task state = %q, want FAILED", task.State)
	}
}

// cannedVerdictPlatform wraps testPlatform and overrides Verify to return a
// fixed result, so the task-lifecycle gate can be pinned without depending on
// the real engine's verdict math (producing a WARN verdict through the engine
// would require a fixture with security findings plus a suppression file).
type cannedVerdictPlatform struct {
	*testPlatform
	res verdict.VerificationResult
}

func (p *cannedVerdictPlatform) Verify(types []string, opts ...verification.Option) verdict.VerificationResult {
	return p.res
}

// TestVerifyWarnCompletesTask pins the exit-code contract on the task gate: a
// WARN verdict is a REPORTED outcome (CLI exit 0), not a failure, so
// TaskService.Verify must COMPLETE the task without error — the task state
// must never contradict the exit code. Regression for the security-suppression
// case: all findings triaged → VerifySecurity returns WARN + "security: OK".
func TestVerifyWarnCompletesTask(t *testing.T) {
	root := t.TempDir()
	p := &cannedVerdictPlatform{
		testPlatform: &testPlatform{root: root, ver: verification.NewEngine(root)},
		res: verdict.VerificationResult{
			Verdict: verdict.VerdictWarn,
			Summary: "security: 8 findings, 8 suppressed (all triaged)",
		},
	}
	ts := NewTaskService(p, nil)

	task, res, err := ts.Verify([]string{"security"})
	if err != nil {
		t.Fatalf("Verify: WARN verdict must not fail the task: %v", err)
	}
	if res.Verdict != verdict.VerdictWarn {
		t.Fatalf("verdict = %q, want WARN", res.Verdict)
	}
	if task.State != domain.TaskCompleted {
		t.Fatalf("task state = %q, want COMPLETED (WARN is a reported outcome, not a failure)", task.State)
	}
	// The warning summary must surface in the completed task's output, not be
	// swallowed by the gate.
	if !strings.Contains(task.Output, "8 suppressed") {
		t.Errorf("task output %q missing warning summary %q", task.Output, res.Summary)
	}
}

// TestVerifySkippedCompletesTask pins the same contract for SKIPPED: a skipped
// check counts as neither passing nor failing in the verdict math, so the task
// completes with the skip reason visible instead of failing (regression for
// the "govulncheck not installed" CVE path).
func TestVerifySkippedCompletesTask(t *testing.T) {
	root := t.TempDir()
	p := &cannedVerdictPlatform{
		testPlatform: &testPlatform{root: root, ver: verification.NewEngine(root)},
		res: verdict.VerificationResult{
			Verdict: verdict.VerdictSkipped,
			Summary: "cve: govulncheck not installed — check skipped",
		},
	}
	ts := NewTaskService(p, nil)

	task, _, err := ts.Verify([]string{"cve"})
	if err != nil {
		t.Fatalf("Verify: SKIPPED verdict must not fail the task: %v", err)
	}
	if task.State != domain.TaskCompleted {
		t.Fatalf("task state = %q, want COMPLETED (SKIPPED is a reported outcome, not a failure)", task.State)
	}
	if !strings.Contains(task.Output, "govulncheck") {
		t.Errorf("task output %q missing skip reason %q", task.Output, "govulncheck")
	}
}

// TestRunWorkflowClassifiesTask verifies that RunWorkflow routes the task
// through the specialist pipeline: the intent is classified into a task kind
// and the kind-selected workflow (which preserves the human approval gate) is
// driven to its approval step. The closed-loop stages remain the execution
// mechanism; the workflow engine provides classification, routing and the
// RequiresApproval gate.
func TestRunWorkflowClassifiesTask(t *testing.T) {
	ts := NewTaskService(&testPlatform{root: t.TempDir(), ver: verification.NewEngine(t.TempDir())}, nil)

	// The intent classifies as a documentation task, whose workflow must
	// preserve the human approval gate before the first execution step.
	intent := "write documentation for the public API"
	kind := agents.ClassifyTask(intent, "")
	if kind != agents.TaskKindDocumentation {
		t.Fatalf("ClassifyTask kind = %d, want documentation", kind)
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
