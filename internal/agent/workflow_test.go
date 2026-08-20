package agent

import (
	"errors"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
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

func TestCompleteApprovalUnknown(t *testing.T) {
	e := NewWorkflowEngine(nil, nil)
	if err := e.CompleteApproval("nope", "human"); err == nil {
		t.Fatal("CompleteApproval(unknown): want error")
	}
}
