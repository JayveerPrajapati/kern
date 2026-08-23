package agent

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestPauseSetsBlockedAndPriorState verifies Pause behaves like Block: it
// records PriorState and transitions to BLOCKED.
func TestPauseSetsBlockedAndPriorState(t *testing.T) {
	tk := NewTask("code", "x")
	if err := tk.Start("agent-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tk.Transition(domain.TaskPlanning); err != nil {
		t.Fatalf("Transition to PLANNING: %v", err)
	}
	if err := tk.Pause("waiting on inputs"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if tk.State != domain.TaskBlocked {
		t.Fatalf("after Pause: state=%s, want BLOCKED", tk.State)
	}
	if tk.PriorState != domain.TaskPlanning {
		t.Fatalf("after Pause: PriorState=%s, want PLANNING", tk.PriorState)
	}
	if tk.Output != "waiting on inputs" {
		t.Fatalf("after Pause: Output=%q, want reason", tk.Output)
	}
	// Pause is idempotent on an already-BLOCKED task.
	if err := tk.Pause("again"); err != nil {
		t.Fatalf("Pause on BLOCKED should be a no-op, got error: %v", err)
	}
	if tk.State != domain.TaskBlocked {
		t.Fatalf("idempotent Pause changed state to %s", tk.State)
	}
}

// TestPauseFailsOnTerminal verifies Pause fails on a truly-terminal task.
func TestPauseFailsOnTerminal(t *testing.T) {
	for _, term := range []domain.TaskState{
		domain.TaskCompleted,
		domain.TaskCancelled,
		domain.TaskRejected,
		domain.TaskRolledBack,
	} {
		tk := NewTask("code", "x")
		tk.State = term
		if err := tk.Pause("x"); err == nil {
			t.Fatalf("Pause on %s: want error (terminal), got nil", term)
		}
	}
}

// TestRetryTracksAttemptReasonAndResult verifies Retry increments RetryCount,
// captures LastResult, and reopens FAILED → ANALYZING. RetryWithReason sets the
// RetryReason. Retry is idempotent when the task is not FAILED.
func TestRetryTracksAttemptReasonAndResult(t *testing.T) {
	tk := NewTask("code", "x")
	if err := tk.Start("agent-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tk.Fail("boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	tk.Output = "first attempt failed output"
	if err := tk.Retry(); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("after Retry: state=%s, want ANALYZING", tk.State)
	}
	if tk.RetryCount != 1 {
		t.Fatalf("after Retry: RetryCount=%d, want 1", tk.RetryCount)
	}
	if tk.LastResult != "first attempt failed output" {
		t.Fatalf("after Retry: LastResult=%q, want prior output", tk.LastResult)
	}
	if tk.RetryReason != "retry requested" {
		t.Fatalf("after Retry: RetryReason=%q, want default", tk.RetryReason)
	}

	// RetryWithReason records the supplied reason.
	if err := tk.Fail("boom again"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := tk.RetryWithReason("transient network error"); err != nil {
		t.Fatalf("RetryWithReason: %v", err)
	}
	if tk.RetryReason != "transient network error" {
		t.Fatalf("after RetryWithReason: RetryReason=%q, want supplied reason", tk.RetryReason)
	}
	if tk.RetryCount != 2 {
		t.Fatalf("after RetryWithReason: RetryCount=%d, want 2", tk.RetryCount)
	}
	if tk.LastResult != "boom again" {
		t.Fatalf("after RetryWithReason: LastResult=%q, want prior output", tk.LastResult)
	}

	// Retry is idempotent when not FAILED (already ANALYZING now).
	if err := tk.Retry(); err != nil {
		t.Fatalf("Retry on non-FAILED should be a no-op, got error: %v", err)
	}
	if tk.RetryCount != 2 {
		t.Fatalf("idempotent Retry changed RetryCount to %d, want 2", tk.RetryCount)
	}
}

// TestRetryFailsOnTerminal verifies Retry fails on a truly-terminal task.
func TestRetryFailsOnTerminal(t *testing.T) {
	tk := NewTask("code", "x")
	tk.State = domain.TaskCompleted
	if err := tk.Retry(); err == nil {
		t.Fatal("Retry on COMPLETED: want error, got nil")
	}
}

// TestTaskAggregateRefsRoundTrip verifies the Phase 1.1 aggregate reference
// fields and the Phase 1.5 retry fields survive a TaskStore JSON save/load.
func TestTaskAggregateRefsRoundTrip(t *testing.T) {
	tk := NewTask("code", "implement foo")
	tk.Project = "myproject"
	tk.Repository = "kern"
	tk.Scope = "package"
	tk.Requester = "human"
	tk.MemoryRef = "mem-1"
	tk.PolicyRef = "pol-1"
	tk.ApprovalRef = "appr-1"
	tk.DeploymentRef = "deploy-1"
	tk.OutcomeRef = "out-1"
	tk.RetryCount = 3
	tk.RetryReason = "flaky"
	tk.LastResult = "prev-out"

	store := NewTaskStore(t.TempDir())
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if loaded.Project != "myproject" {
		t.Errorf("Project = %q, want myproject", loaded.Project)
	}
	if loaded.Repository != "kern" {
		t.Errorf("Repository = %q, want kern", loaded.Repository)
	}
	if loaded.Scope != "package" {
		t.Errorf("Scope = %q, want package", loaded.Scope)
	}
	if loaded.Requester != "human" {
		t.Errorf("Requester = %q, want human", loaded.Requester)
	}
	if loaded.MemoryRef != "mem-1" {
		t.Errorf("MemoryRef = %q, want mem-1", loaded.MemoryRef)
	}
	if loaded.PolicyRef != "pol-1" {
		t.Errorf("PolicyRef = %q, want pol-1", loaded.PolicyRef)
	}
	if loaded.ApprovalRef != "appr-1" {
		t.Errorf("ApprovalRef = %q, want appr-1", loaded.ApprovalRef)
	}
	if loaded.DeploymentRef != "deploy-1" {
		t.Errorf("DeploymentRef = %q, want deploy-1", loaded.DeploymentRef)
	}
	if loaded.OutcomeRef != "out-1" {
		t.Errorf("OutcomeRef = %q, want out-1", loaded.OutcomeRef)
	}
	if loaded.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", loaded.RetryCount)
	}
	if loaded.RetryReason != "flaky" {
		t.Errorf("RetryReason = %q, want flaky", loaded.RetryReason)
	}
	if loaded.LastResult != "prev-out" {
		t.Errorf("LastResult = %q, want prev-out", loaded.LastResult)
	}
}