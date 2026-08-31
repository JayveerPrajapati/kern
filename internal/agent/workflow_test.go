package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

func setupWorkflowRegistry() *Registry {
	r := NewRegistry()
	_ = r.Register(Agent{Agent: mkAgent("p1", "Planner", "planner").Agent})
	_ = r.Register(Agent{Agent: mkAgent("c1", "Coder", "coder").Agent})
	_ = r.Register(Agent{Agent: mkAgent("r1", "Reviewer", "reviewer").Agent})
	return r
}

// runThroughApproval runs the default workflow on tk; if Run pauses for human
// approval it approves via the engine and re-runs.
func runThroughApproval(t *testing.T, e *WorkflowEngine, tk *Task, handler func(string, *Task) (string, error)) (*Task, error) {
	t.Helper()
	task, err := e.Run(tk, handler)
	if errors.Is(err, ErrApprovalRequired) {
		if err := e.CompleteApproval(ApprovalID(err), "human"); err != nil {
			t.Fatalf("CompleteApproval: %v", err)
		}
		return e.Run(tk, handler)
	}
	return task, err
}

func TestDefaultWorkflowSteps(t *testing.T) {
	wf := DefaultWorkflow()
	actions := []string{"request", "analyze", "plan", "approve", "code", "verify", "pr"}
	if len(wf.Steps) != len(actions) {
		t.Fatalf("DefaultWorkflow has %d steps, want %d", len(wf.Steps), len(actions))
	}
	for i, want := range actions {
		if wf.Steps[i].Action != want {
			t.Fatalf("step %d action = %q, want %q", i, wf.Steps[i].Action, want)
		}
	}
	if !wf.Steps[3].RequiresApproval {
		t.Fatal("approve step should require approval")
	}
}

func TestRegisterGetWorkflow(t *testing.T) {
	e := NewWorkflowEngine(nil, nil)
	wf := Workflow{ID: "custom", Name: "custom wf", Steps: []WorkflowStep{{Action: "code", AgentType: "coder"}}}
	e.RegisterWorkflow(wf)
	got, ok := e.GetWorkflow("custom")
	if !ok {
		t.Fatal("GetWorkflow(custom): not found")
	}
	if got.Name != "custom wf" {
		t.Fatalf("GetWorkflow name = %q, want custom wf", got.Name)
	}
	if _, ok := e.GetWorkflow("nope"); ok {
		t.Fatal("GetWorkflow(nope): found; want not found")
	}
}

func TestRunHappyPath(t *testing.T) {
	e := NewWorkflowEngine(setupWorkflowRegistry(), nil)
	tk := NewTask("code", "feature")
	handler := func(action string, task *Task) (string, error) {
		return "done:" + action, nil
	}
	got, err := runThroughApproval(t, e, tk, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.State != domain.TaskCompleted {
		t.Fatalf("state = %s, want COMPLETED", got.State)
	}
	// The step log records the executable steps plus the blocked approval
	// gate recorded when Run first paused: request, analyze, plan, approve
	// (blocked), code, verify, pr.
	wantActions := []string{"request", "analyze", "plan", "approve", "code", "verify", "pr"}
	if len(got.Steps) != len(wantActions) {
		t.Fatalf("step log len = %d, want %d", len(got.Steps), len(wantActions))
	}
	for i, want := range wantActions {
		if got.Steps[i].Action != want {
			t.Fatalf("step %d action = %q, want %q", i, got.Steps[i].Action, want)
		}
	}
	if got.Steps[3].Status != "blocked" {
		t.Fatalf("approval step status = %q, want blocked", got.Steps[3].Status)
	}
}

func TestRunFailingHandler(t *testing.T) {
	e := NewWorkflowEngine(setupWorkflowRegistry(), nil)
	tk := NewTask("code", "feature")
	handler := func(action string, task *Task) (string, error) {
		if action == "code" {
			return "", errors.New("code failed")
		}
		return "ok:" + action, nil
	}
	task, err := runThroughApproval(t, e, tk, handler)
	if err == nil {
		t.Fatal("Run: expected error from failing handler, got nil")
	}
	if err.Error() != "code failed" {
		t.Fatalf("Run error = %v, want code failed", err)
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("state = %s, want FAILED", task.State)
	}
	if task.Steps[len(task.Steps)-1].Status != "failed" {
		t.Fatalf("last step status = %q, want failed", task.Steps[len(task.Steps)-1].Status)
	}
}

func TestRunApprovalRequired(t *testing.T) {
	e := NewWorkflowEngine(setupWorkflowRegistry(), nil)
	tk := NewTask("code", "feature")
	handler := func(action string, task *Task) (string, error) { return "ok", nil }

	_, err := e.Run(tk, handler)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("Run: expected ErrApprovalRequired, got %v", err)
	}
	if id := ApprovalID(err); id == "" {
		t.Fatal("ApprovalID(err): empty, want a pending approval ID")
	}
	if tk.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %s, want WAITING_FOR_APPROVAL", tk.State)
	}
	if tk.Steps[len(tk.Steps)-1].Status != "blocked" {
		t.Fatalf("last step status = %q, want blocked", tk.Steps[len(tk.Steps)-1].Status)
	}
}

func TestRunNoRegisteredAgentFailsClosed(t *testing.T) {
	// Registry lacks a "coder" agent → the workflow must fail at the "code"
	// step rather than silently succeed.
	e := NewWorkflowEngine(NewRegistry(), nil)
	_ = e.registry.Register(Agent{Agent: mkAgent("p1", "Planner", "planner").Agent})
	_ = e.registry.Register(Agent{Agent: mkAgent("r1", "Reviewer", "reviewer").Agent})
	tk := NewTask("code", "feature")
	handler := func(action string, task *Task) (string, error) { return "ok", nil }
	task, err := runThroughApproval(t, e, tk, handler)
	if err == nil {
		t.Fatal("expected error for missing coder agent")
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("state = %s, want FAILED", task.State)
	}
}

func TestRunNilInputs(t *testing.T) {
	e := NewWorkflowEngine(nil, nil)
	if _, err := e.Run(nil, func(string, *Task) (string, error) { return "", nil }); err == nil {
		t.Fatal("Run(nil task): want error")
	}
	if _, err := e.Run(NewTask("code", "x"), nil); err == nil {
		t.Fatal("Run(nil handler): want error")
	}
}

// TestLargeStepOutputNormalized verifies tool-output normalization in
// the workflow engine: a step that produces a large raw output (over the 4 KiB
// threshold) records a compact normalized summary in its step history while the
// raw output remains on the task's Output (available outside active context).
func TestLargeStepOutputNormalized(t *testing.T) {
	e := NewWorkflowEngine(setupWorkflowRegistry(), nil)
	tk := NewTask("code", "feature")
	// Produce a large output that mixes errors with noise — the classic
	// "large test output" case.
	big := strings.Repeat("ok line\n", 500)
	big += "main.go:42: error: undefined: Bar\n" + strings.Repeat("noise\n", 500)
	handler := func(action string, task *Task) (string, error) {
		// "code" and the final "pr" step both return the large raw output so
		// the task's final Output preserves it (the engine sets Output per
		// step, so the last step wins).
		if action == "code" || action == "pr" {
			return big, nil
		}
		return "small", nil
	}
	task, err := runThroughApproval(t, e, tk, handler)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Find the "code" step: it must be normalized.
	var codeStep *Step
	for i := range task.Steps {
		if task.Steps[i].Action == "code" {
			codeStep = &task.Steps[i]
			break
		}
	}
	if codeStep == nil {
		t.Fatal("no code step recorded")
	}
	if len(codeStep.Result) >= len(big) {
		t.Fatalf("step result not normalized: %d chars, want < raw %d", len(codeStep.Result), len(big))
	}
	if !strings.Contains(codeStep.Result, "[normalized") {
		t.Errorf("step result missing normalized marker: %q", codeStep.Result)
	}
	// Errors must be retained in the normalized summary.
	if !strings.Contains(codeStep.Result, "error: undefined: Bar") {
		t.Errorf("normalized step result lost the error line: %q", codeStep.Result)
	}
	// The raw output must remain available on the task (outside active context).
	if task.Output != big {
		t.Errorf("task.Output lost the raw output (%d chars, want %d)", len(task.Output), len(big))
	}
	// A small step output must be stored verbatim (no normalization).
	for i := range task.Steps {
		if task.Steps[i].Action == "analyze" && task.Steps[i].Result != "small" {
			t.Errorf("analyze step result = %q, want verbatim 'small'", task.Steps[i].Result)
		}
	}
}

func TestCompleteApprovalUnknown(t *testing.T) {
	e := NewWorkflowEngine(nil, nil)
	if err := e.CompleteApproval("nope", "human"); err == nil {
		t.Fatal("CompleteApproval(unknown): want error")
	}
}

// TestApprovalGatePersistsAcrossEngine verifies the resume contract:
// a run that parks at an approval gate records its resume step + approval
// binding on the task, and a FRESH engine (same task) observes the out-of-band
// approval decision from the persistent store and drives past the gate.
func TestApprovalGatePersistsAcrossEngine(t *testing.T) {
	root := t.TempDir()
	store := governance.NewFileStore(root)

	// Engine 1 parks the run at the gate.
	e1 := NewWorkflowEngine(setupWorkflowRegistry(), nil).WithApprovalStore(store)
	tk := NewTask("code", "feature")
	tk.WorkflowID = "default"
	handler := func(action string, task *Task) (string, error) { return "ok", nil }
	_, err := e1.Run(tk, handler)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("Run: expected ErrApprovalRequired, got %v", err)
	}
	id := ApprovalID(err)
	if id == "" {
		t.Fatal("ApprovalID(err): empty")
	}
	// The run state must be persisted on the task: resume step + binding.
	if tk.ResumeStep <= 0 {
		t.Fatalf("ResumeStep = %d, want > 0 (persisted gate position)", tk.ResumeStep)
	}
	if tk.ApprovalRefs[id] != tk.ResumeStep {
		t.Fatalf("ApprovalRefs[%s] = %d, want %d", id, tk.ApprovalRefs[id], tk.ResumeStep)
	}

	// Out-of-band approval through the persistent store (what `kern approve`
	// writes)...
	if _, err := store.Decide(id, "human", true, ""); err != nil {
		t.Fatalf("store.Decide: %v", err)
	}

	// ...and a FRESH engine resumes the SAME task, sees the approved gate, and
	// completes the workflow (code → verify → pr).
	e2 := NewWorkflowEngine(setupWorkflowRegistry(), nil).WithApprovalStore(store)
	tk2 := NewTask("code", "feature")
	tk2.WorkflowID = "default"
	tk2.ID = tk.ID // resume the same task identity
	tk2.ResumeStep = tk.ResumeStep
	tk2.ApprovalRefs = tk.ApprovalRefs
	tk2.State = tk.State
	final, err := e2.Run(tk2, handler)
	if err != nil {
		t.Fatalf("fresh-engine resume: %v", err)
	}
	if !final.Terminal() {
		t.Fatalf("state = %s, want terminal after fresh-engine resume", final.State)
	}
	if final.ResumeStep != 0 || final.ApprovalRefs != nil {
		t.Errorf("resume state not cleared on completion: ResumeStep=%d ApprovalRefs=%v", final.ResumeStep, final.ApprovalRefs)
	}
}
