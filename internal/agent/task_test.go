package agent

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestNewTaskDefaults(t *testing.T) {
	tk := NewTask("code", "implement foo")
	if tk.ID == "" {
		t.Fatal("NewTask: empty ID")
	}
	if tk.Type != "code" || tk.Input != "implement foo" {
		t.Fatalf("NewTask: type=%q input=%q", tk.Type, tk.Input)
	}
	if tk.State != domain.TaskCreated {
		t.Fatalf("NewTask: state = %s, want CREATED", tk.State)
	}
	if tk.CreatedAt.IsZero() || tk.UpdatedAt.IsZero() {
		t.Fatal("NewTask: timestamps not set")
	}
}

func TestTaskStructuredResultFields(t *testing.T) {
	tk := NewTask("code", "implement feature")
	tk.Evidence = []domain.Claim{{Type: domain.ClaimFact, Statement: "build passed"}}
	tk.Risks = []domain.Risk{{Level: domain.RiskMedium}}
	tk.Confidence = 0.92
	tk.RecommendedAction = "continue"

	if len(tk.Evidence) != 1 || tk.Evidence[0].Type != domain.ClaimFact {
		t.Errorf("Evidence = %+v, want 1 FACT claim", tk.Evidence)
	}
	if len(tk.Risks) != 1 || tk.Risks[0].Level != domain.RiskMedium {
		t.Errorf("Risks = %+v, want 1 MEDIUM risk", tk.Risks)
	}
	if tk.Confidence != 0.92 {
		t.Errorf("Confidence = %v, want 0.92", tk.Confidence)
	}
	if tk.RecommendedAction != "continue" {
		t.Errorf("RecommendedAction = %q, want \"continue\"", tk.RecommendedAction)
	}
}

func TestTaskStartCompleteFail(t *testing.T) {
	tk := NewTask("code", "x")
	if err := tk.Start("agent-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("after Start: state=%s, want ANALYZING", tk.State)
	}
	if tk.AgentID != "agent-1" {
		t.Fatalf("after Start: AgentID=%q", tk.AgentID)
	}
	// Complete is only legal from states that transition to COMPLETED, so
	// drive the task through the state machine to PR_CREATED first.
	path := []domain.TaskState{
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskReadyForPR,
		domain.TaskPRCreated,
	}
	for _, s := range path {
		if err := tk.Transition(s); err != nil {
			t.Fatalf("Transition(%s): %v", s, err)
		}
	}
	if err := tk.Complete("done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if tk.State != domain.TaskCompleted || tk.Output != "done" {
		t.Fatalf("after Complete: state=%s output=%q", tk.State, tk.Output)
	}
	if !tk.IsTerminal() {
		t.Fatal("after Complete: task not terminal")
	}

	tk2 := NewTask("code", "y")
	if err := tk2.Fail("boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if tk2.State != domain.TaskFailed || tk2.Output != "boom" {
		t.Fatalf("after Fail: state=%s output=%q", tk2.State, tk2.Output)
	}
	if !tk2.IsTerminal() {
		t.Fatal("after Fail: task not terminal")
	}
}

func TestCompleteFromNonTerminalStateRejected(t *testing.T) {
	// The approval gate must not be bypassable for code-change tasks: COMPLETED
	// is only reachable through the state table, not by calling Complete from an
	// execution state. Read-only tasks (analyze, verify) MAY complete directly
	// from their respective states — that is tested separately below.
	tk := NewTask("code", "x")
	// Drive to EXECUTING (the code-change path) so we test the gate at the
	// point where bypass would be dangerous: a code change skipping verify.
	for _, s := range []domain.TaskState{
		domain.TaskAnalyzing,
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
	} {
		if err := tk.Transition(s); err != nil {
			t.Fatalf("Transition to %s: %v", s, err)
		}
	}
	if err := tk.Complete("done"); err == nil {
		t.Fatal("Complete from EXECUTING: want error (gate bypass guard)")
	}
	if tk.State == domain.TaskCompleted {
		t.Fatal("Complete from EXECUTING reached COMPLETED; want no state change")
	}
}

func TestReadOnlyTaskCompletesFromAnalyzing(t *testing.T) {
	// Read-only tasks (analyze) may complete directly from ANALYZING without
	// driving the full code-change lifecycle — there is nothing to approve,
	// execute, or PR. This is the Phase 2 Task-authoritative path for
	// kern analyze / kern_analyze.
	tk := NewTask("analyze", "x")
	if err := tk.Start("context-engine"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tk.Complete("analysis done"); err != nil {
		t.Fatalf("Complete from ANALYZING for read-only task: %v", err)
	}
	if tk.State != domain.TaskCompleted {
		t.Fatalf("state = %s; want COMPLETED", tk.State)
	}
}

func TestTaskTransitionValid(t *testing.T) {
	path := []struct {
		from domain.TaskState
		to   domain.TaskState
	}{
		{domain.TaskCreated, domain.TaskAnalyzing},
		{domain.TaskAnalyzing, domain.TaskPlanning},
		{domain.TaskPlanning, domain.TaskWaitingApproval},
		{domain.TaskWaitingApproval, domain.TaskApproved},
		{domain.TaskApproved, domain.TaskExecuting},
		{domain.TaskExecuting, domain.TaskVerifying},
		{domain.TaskVerifying, domain.TaskReadyForPR},
		{domain.TaskReadyForPR, domain.TaskPRCreated},
		{domain.TaskPRCreated, domain.TaskCompleted},
	}
	tk := NewTask("code", "x")
	for _, p := range path {
		tk.State = p.from
		if err := tk.Transition(p.to); err != nil {
			t.Fatalf("valid transition %s->%s: unexpected error %v", p.from, p.to, err)
		}
		if tk.State != p.to {
			t.Fatalf("transition %s->%s left state %s", p.from, p.to, tk.State)
		}
	}
}

func TestTransitionInvalid(t *testing.T) {
	cases := []struct {
		from, to domain.TaskState
	}{
		{domain.TaskCreated, domain.TaskCompleted},   // skip
		{domain.TaskAnalyzing, domain.TaskExecuting}, // skip
		{domain.TaskPlanning, domain.TaskExecuting},  // skip (Plan completes or waits for approval, not execute)
		{domain.TaskExecuting, domain.TaskPlanning},  // backward
	}
	for _, c := range cases {
		tk := NewTask("code", "x")
		tk.State = c.from
		if err := tk.Transition(c.to); err == nil {
			t.Fatalf("invalid transition %s->%s: want error, got nil", c.from, c.to)
		}
	}
}

func TestTransitionFromTerminalRejected(t *testing.T) {
	terminals := []domain.TaskState{
		domain.TaskCompleted,
		domain.TaskFailed,
		domain.TaskBlocked,
		domain.TaskRejected,
		domain.TaskCancelled,
		domain.TaskRolledBack,
	}
	for _, term := range terminals {
		tk := NewTask("code", "x")
		tk.State = term
		// Try to move to a normally-allowed state.
		if err := tk.Transition(domain.TaskAnalyzing); err == nil {
			t.Fatalf("transition from terminal %s: want error, got nil", term)
		}
		if tk.State != term {
			t.Fatalf("terminal %s was mutated to %s", term, tk.State)
		}
	}
}

func TestCompleteFromTerminalFails(t *testing.T) {
	tk := NewTask("code", "x")
	// Drive to a completable state first (Complete only legal from PR_CREATED).
	path := []domain.TaskState{
		domain.TaskAnalyzing,
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskReadyForPR,
		domain.TaskPRCreated,
	}
	for _, s := range path {
		if err := tk.Transition(s); err != nil {
			t.Fatalf("Transition(%s): %v", s, err)
		}
	}
	if err := tk.Complete("done"); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := tk.Complete("again"); err == nil {
		t.Fatal("second Complete on terminal task: want error")
	}
}

func TestAddStep(t *testing.T) {
	tk := NewTask("code", "x")
	tk.AddStep(Step{Action: "plan", AgentID: "p", Status: "success"})
	tk.AddStep(Step{Action: "code", AgentID: "c", Status: "success"})
	if len(tk.Steps) != 2 {
		t.Fatalf("len(Steps)=%d, want 2", len(tk.Steps))
	}
	if tk.Steps[0].Index != 1 || tk.Steps[1].Index != 2 {
		t.Fatalf("step indices = %d,%d, want 1,2", tk.Steps[0].Index, tk.Steps[1].Index)
	}
	if tk.Steps[0].Action != "plan" || tk.Steps[1].Action != "code" {
		t.Fatalf("step actions = %q,%q", tk.Steps[0].Action, tk.Steps[1].Action)
	}
}
