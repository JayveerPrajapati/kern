package agent

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestCancelFromRunningState verifies Cancel transitions a running task to
// CANCELLED.
func TestCancelFromRunningState(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1") // CREATED → ANALYZING
	if err := tk.Cancel("user requested"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if tk.State != domain.TaskCancelled {
		t.Fatalf("state=%s, want CANCELLED", tk.State)
	}
	if tk.Output != "user requested" {
		t.Fatalf("output=%q, want reason", tk.Output)
	}
}

// TestCancelFromTerminalFails verifies Cancel is rejected on truly-terminal
// states.
func TestCancelFromTerminalFails(t *testing.T) {
	for _, term := range []domain.TaskState{domain.TaskCompleted, domain.TaskRejected, domain.TaskRolledBack} {
		tk := NewTask("code", "x")
		tk.State = term
		if err := tk.Cancel("nope"); err == nil {
			t.Fatalf("Cancel from %s: want error, got nil", term)
		}
	}
}

// TestRetryFromFailed verifies Retry reopens a FAILED task to ANALYZING.
func TestRetryFromFailed(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1")
	if err := tk.Fail("boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if tk.State != domain.TaskFailed {
		t.Fatalf("state=%s, want FAILED", tk.State)
	}
	if err := tk.Retry(); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("after Retry: state=%s, want ANALYZING", tk.State)
	}
}

// TestRetryIdempotency verifies that retrying a non-FAILED task is a no-op.
func TestRetryIdempotency(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1") // ANALYZING
	// Retry on a running task should be a no-op (no error, no state change).
	prev := tk.State
	if err := tk.Retry(); err != nil {
		t.Fatalf("Retry on running task: %v", err)
	}
	if tk.State != prev {
		t.Fatalf("Retry changed state: %s → %s (should be no-op)", prev, tk.State)
	}
	// Retry again — still no-op.
	if err := tk.Retry(); err != nil {
		t.Fatalf("second Retry: %v", err)
	}
	if tk.State != prev {
		t.Fatalf("second Retry changed state: %s → %s", prev, tk.State)
	}
}

// TestRetryFromTerminalFails verifies Retry is rejected on truly-terminal
// states (COMPLETED, CANCELLED, REJECTED, ROLLED_BACK).
func TestRetryFromTerminalFails(t *testing.T) {
	for _, term := range []domain.TaskState{domain.TaskCompleted, domain.TaskCancelled, domain.TaskRejected, domain.TaskRolledBack} {
		tk := NewTask("code", "x")
		tk.State = term
		if err := tk.Retry(); err == nil {
			t.Fatalf("Retry from %s: want error, got nil", term)
		}
	}
}

// TestResumeFromBlocked verifies Resume returns a BLOCKED task to its
// PriorState.
func TestResumeFromBlocked(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1")                // CREATED → ANALYZING
	_ = tk.Transition(domain.TaskPlanning) // ANALYZING → PLANNING
	if err := tk.Block("waiting for input"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if tk.State != domain.TaskBlocked {
		t.Fatalf("state=%s, want BLOCKED", tk.State)
	}
	if tk.PriorState != domain.TaskPlanning {
		t.Fatalf("PriorState=%s, want PLANNING", tk.PriorState)
	}
	if err := tk.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if tk.State != domain.TaskPlanning {
		t.Fatalf("after Resume: state=%s, want PLANNING (PriorState)", tk.State)
	}
}

// TestResumeIdempotency verifies that resuming a non-BLOCKED task is a no-op.
func TestResumeIdempotency(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1") // ANALYZING
	prev := tk.State
	if err := tk.Resume(); err != nil {
		t.Fatalf("Resume on running task: %v", err)
	}
	if tk.State != prev {
		t.Fatalf("Resume changed state: %s → %s (should be no-op)", prev, tk.State)
	}
}

// TestTimeout verifies Timeout transitions to FAILED.
func TestTimeout(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("agent-1")
	if err := tk.Timeout(); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	if tk.State != domain.TaskFailed {
		t.Fatalf("state=%s, want FAILED", tk.State)
	}
}

// TestRollback verifies Rollback transitions from PR_CREATED to ROLLED_BACK.
func TestRollback(t *testing.T) {
	tk := NewTask("code", "x")
	for _, s := range []domain.TaskState{
		domain.TaskAnalyzing,
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskReadyForPR,
		domain.TaskPRCreated,
	} {
		if err := tk.Transition(s); err != nil {
			t.Fatalf("Transition(%s): %v", s, err)
		}
	}
	if err := tk.Rollback("bad deploy"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if tk.State != domain.TaskRolledBack {
		t.Fatalf("state=%s, want ROLLED_BACK", tk.State)
	}
	if tk.Output != "bad deploy" {
		t.Fatalf("output=%q, want reason", tk.Output)
	}
}

// TestHumanTakeover verifies HumanTakeover blocks the task and binds the
// human agent, with PriorState preserved for resume.
func TestHumanTakeover(t *testing.T) {
	tk := NewTask("code", "x")
	_ = tk.Start("bot-1") // ANALYZING
	if err := tk.HumanTakeover("human-1"); err != nil {
		t.Fatalf("HumanTakeover: %v", err)
	}
	if tk.State != domain.TaskBlocked {
		t.Fatalf("state=%s, want BLOCKED", tk.State)
	}
	if tk.AgentID != "human-1" {
		t.Fatalf("AgentID=%q, want human-1", tk.AgentID)
	}
	if tk.PriorState != domain.TaskAnalyzing {
		t.Fatalf("PriorState=%s, want ANALYZING", tk.PriorState)
	}
	// After human intervention, resume returns to ANALYZING.
	if err := tk.Resume(); err != nil {
		t.Fatalf("Resume after HumanTakeover: %v", err)
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("after Resume: state=%s, want ANALYZING", tk.State)
	}
}

// TestRestartResume verifies a task persists to the TaskStore, reloads after a
// "restart" (new TaskStore instance pointing at the same path), and can be
// resumed from BLOCKED.
func TestRestartResume(t *testing.T) {
	store := NewTaskStore(t.TempDir())
	tk := NewTask("code", "add caching")
	_ = tk.Start("agent-1") // CREATED → ANALYZING
	// Drive to EXECUTING via the legal path.
	for _, s := range []domain.TaskState{
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
	} {
		if err := tk.Transition(s); err != nil {
			t.Fatalf("Transition(%s): %v", s, err)
		}
	}
	if err := tk.Block("needs human input"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate a process restart: create a new TaskStore at the same path.
	store2 := NewTaskStore(store.root)
	loaded, err := store2.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if loaded.State != domain.TaskBlocked {
		t.Fatalf("after restart: state=%s, want BLOCKED", loaded.State)
	}
	if loaded.PriorState != domain.TaskExecuting {
		t.Fatalf("after restart: PriorState=%s, want EXECUTING", loaded.PriorState)
	}
	// Resume the reloaded task.
	rt := loaded
	if err := rt.Resume(); err != nil {
		t.Fatalf("Resume after restart: %v", err)
	}
	if rt.State != domain.TaskExecuting {
		t.Fatalf("after Resume: state=%s, want EXECUTING", rt.State)
	}
}
