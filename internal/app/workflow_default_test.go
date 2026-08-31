package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestRunWorkflowDefaultSequencesTeam verifies the exit gate: Kern
// selects and coordinates the agent team without the external caller manually
// sequencing it. RunWorkflowDefault with only an intent must classify the task,
// register the standard specialist team, drive the workflow steps (analyze →
// plan → [approval gate] → code → verify → pr), record real steps and
// artifacts, and park at the human approval gate before execution.
func TestRunWorkflowDefaultSequencesTeam(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module teamfixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// Run 1: kern sequences the team; the workflow must park at the human
	// approval gate (Invariant #2: high-risk execution requires approval).
	task, err := ts.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require human approval before execution")
	}
	if !errors.Is(err, agent.ErrApprovalRequired) {
		t.Fatalf("error = %v, want ErrApprovalRequired", err)
	}
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %q, want WAITING_FOR_APPROVAL", task.State)
	}

	// The selection + coordination must have run real steps before the gate:
	// request → analyze → plan, with the analyze output attached.
	actions := stepActions(task)
	if len(actions) < 3 {
		t.Fatalf("steps = %v, want at least request/analyze/plan before the gate", actions)
	}
	if actions[0] != "request" || actions[1] != "analyze" || actions[2] != "plan" {
		t.Errorf("first steps = %v, want request, analyze, plan", actions)
	}
	if task.ContextPacket == nil {
		t.Error("analyze step did not attach the context packet (real deterministic analysis)")
	}
	if task.Plan == nil {
		t.Error("plan step did not attach the plan (real deterministic plan assembly)")
	}
	if len(task.Evidence) == 0 {
		t.Error("analyze step did not attach evidence claims")
	}

	// Resolve the approval gate and resume: the engine must resume at the gate
	// and drive code → verify → pr to completion.
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced from the approval gate")
	}
	if err := ts.CompleteApproval(approvalID, "test-human"); err != nil {
		t.Fatalf("CompleteApproval: %v", err)
	}
	task, err = ts.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume after approval: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Fatalf("state = %q, want COMPLETED after approval + re-run", task.State)
	}
	final := stepActions(task)
	for _, want := range []string{"code", "verify", "pr"} {
		if !contains(final, want) {
			t.Errorf("final steps missing %q (got %v)", want, final)
		}
	}
}

// TestRunWorkflowDefaultIncidentKind verifies kind-based agent selection: an
// incident task selects the incident workflow (planner → approve → coder →
// security → tester → sre → reviewer) rather than the default 7-step one.
func TestRunWorkflowDefaultIncidentKind(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module incfixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, err := ts.RunWorkflowDefault("incident: fix the outage in NewServer")
	if err == nil {
		t.Fatal("expected approval gate on incident workflow")
	}
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %q, want WAITING_FOR_APPROVAL", task.State)
	}

	// Resolve the gate; the incident workflow must then run security, test,
	// and sre stages before the pr step.
	approvalID := agent.ApprovalID(err)
	if err := ts.CompleteApproval(approvalID, "test-human"); err != nil {
		t.Fatalf("CompleteApproval: %v", err)
	}
	task, err = ts.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume after approval: %v", err)
	}
	final := stepActions(task)
	for _, want := range []string{"security", "test", "sre"} {
		if !contains(final, want) {
			t.Errorf("incident workflow steps missing %q (got %v)", want, final)
		}
	}
}

// TestRunWorkflowDefaultCrossProcessResume verifies the exit gate's
// durability: a run that parks at the human approval gate survives PROCESS
// boundaries. A fresh TaskService (simulating a new CLI/MCP invocation) must
// recover the parked task from the TaskStore, rebuild the engine from the
// task's persisted workflow, observe the out-of-band approval decision in the
// persistent approval store, and drive the remaining steps to completion.
func TestRunWorkflowDefaultCrossProcessResume(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module crossfixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")

	// "Process 1": create the run, park at the gate, persist task + approval.
	p1, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts1 := NewTaskService(p1, nil).WithAgentID("test")
	task1, err := ts1.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("expected approval gate")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced")
	}

	// "Process 2": out-of-band approval via the persistent store (what
	// `kern approve` does), then a FRESH TaskService resumes the run.
	store := governance.NewFileStore(root)
	if _, err := store.Decide(approvalID, "cli-user", true, ""); err != nil {
		t.Fatalf("out-of-band approve: %v", err)
	}
	p2, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts2 := NewTaskService(p2, nil).WithAgentID("test")
	task2, err := ts2.RunWorkflowResume(task1.ID)
	if err != nil {
		t.Fatalf("cross-process resume: %v", err)
	}
	if string(task2.State) != "COMPLETED" {
		t.Fatalf("state = %q, want COMPLETED after cross-process resume", task2.State)
	}
	// The resumed run must have completed the execution stages, not re-parked.
	final := stepActions(task2)
	for _, want := range []string{"code", "verify", "pr"} {
		if !contains(final, want) {
			t.Errorf("resumed steps missing %q (got %v)", want, final)
		}
	}
}

// stepActions extracts the ordered step actions from a task's step history.
func stepActions(t *agent.Task) []string {
	var out []string
	for _, st := range t.Steps {
		out = append(out, st.Action)
	}
	return out
}

// contains reports whether s is in the slice.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestStageOutcomeDeterministic verifies the coordination-level stage outcomes
// are derived from real task data and never empty.
func TestStageOutcomeDeterministic(t *testing.T) {
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil)
	task := &agent.Task{}
	task.Risks = []domain.Risk{{Level: domain.RiskHigh, Mitigation: "m"}}
	task.Plan = &domain.Plan{ImplementationSteps: []string{"s1"}, AffectedComponents: []string{"a"}, Tests: []string{"t1"}}
	for _, action := range []string{"code", "verify", "pr", "review", "security", "test", "sre", "architect"} {
		out := ts.stageOutcome(action, task)
		if strings.TrimSpace(out) == "" {
			t.Errorf("stageOutcome(%q) is empty", action)
		}
		if !strings.Contains(out, action) {
			t.Errorf("stageOutcome(%q) = %q, want action named in outcome", action, out)
		}
	}
}
