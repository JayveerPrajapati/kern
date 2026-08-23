package agent

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestTaskAggregateRefsJSONRoundTrip verifies the Phase 1.1 plural aggregate
// reference fields survive a TaskStore JSON save/load.
func TestTaskAggregateRefsJSONRoundTrip(t *testing.T) {
	tk := NewTask("code", "implement foo")
	tk.AgentRefs = []string{"bot-1", "bot-2"}
	tk.MemoryRefs = []string{"mem-a", "mem-b"}
	tk.ContextRef = "ctx-1"
	tk.ImpactRef = "imp-1"
	tk.RiskRef = "risk-1"
	tk.PlanRef = "plan-1"
	tk.VerificationRef = "ver-1"
	tk.PRRef = "pr-1"
	tk.LearningRef = "learn-1"

	store := NewTaskStore(t.TempDir())
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertStrings(t, "AgentRefs", loaded.AgentRefs, tk.AgentRefs)
	assertStrings(t, "MemoryRefs", loaded.MemoryRefs, tk.MemoryRefs)
	if loaded.ContextRef != "ctx-1" {
		t.Errorf("ContextRef = %q, want ctx-1", loaded.ContextRef)
	}
	if loaded.ImpactRef != "imp-1" {
		t.Errorf("ImpactRef = %q, want imp-1", loaded.ImpactRef)
	}
	if loaded.RiskRef != "risk-1" {
		t.Errorf("RiskRef = %q, want risk-1", loaded.RiskRef)
	}
	if loaded.PlanRef != "plan-1" {
		t.Errorf("PlanRef = %q, want plan-1", loaded.PlanRef)
	}
	if loaded.VerificationRef != "ver-1" {
		t.Errorf("VerificationRef = %q, want ver-1", loaded.VerificationRef)
	}
	if loaded.PRRef != "pr-1" {
		t.Errorf("PRRef = %q, want pr-1", loaded.PRRef)
	}
	if loaded.LearningRef != "learn-1" {
		t.Errorf("LearningRef = %q, want learn-1", loaded.LearningRef)
	}
}

func assertStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
		}
	}
}

// TestTaskSnapshotMethod verifies Snapshot() derives Goal and State.
func TestTaskSnapshotMethod(t *testing.T) {
	tk := NewTask("code", "input text")
	tk.Intent = "intent text"
	if err := tk.Start("bot-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := tk.Snapshot()
	if snap.Goal != "intent text" {
		t.Errorf("Goal = %q, want intent text", snap.Goal)
	}
	if snap.State != string(domain.TaskAnalyzing) {
		t.Errorf("State = %q, want ANALYZING", snap.State)
	}
	if snap.NextAction != "" {
		t.Errorf("NextAction = %q, want empty", snap.NextAction)
	}

	// Fallback to Input when Intent is empty.
	tk2 := NewTask("code", "input only")
	_ = tk2.Start("bot-1")
	snap2 := tk2.Snapshot()
	if snap2.Goal != "input only" {
		t.Errorf("Goal (fallback) = %q, want input only", snap2.Goal)
	}

	// Risks are derived from the task's risks (Level, no String method).
	tk3 := NewTask("code", "risky")
	tk3.Risks = []domain.Risk{
		{Level: domain.RiskHigh},
		{Level: domain.RiskLow},
	}
	snap3 := tk3.Snapshot()
	if len(snap3.Risks) != 2 || snap3.Risks[0] != "HIGH" || snap3.Risks[1] != "LOW" {
		t.Errorf("Risks = %v, want [HIGH LOW]", snap3.Risks)
	}
}

// TestReturnToAgent verifies human-takeover return semantics.
func TestReturnToAgent(t *testing.T) {
	tk := NewTask("code", "x")
	if err := tk.Start("bot-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// HumanTakeover → BLOCKED with AgentID "human".
	if err := tk.HumanTakeover("human"); err != nil {
		t.Fatalf("HumanTakeover: %v", err)
	}
	if tk.State != domain.TaskBlocked {
		t.Fatalf("State = %s, want BLOCKED", tk.State)
	}
	// ReturnToAgent resumes to PriorState (ANALYZING) and rebinds.
	if err := tk.ReturnToAgent("bot-2"); err != nil {
		t.Fatalf("ReturnToAgent: %v", err)
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("State = %s, want ANALYZING", tk.State)
	}
	if tk.AgentID != "bot-2" {
		t.Fatalf("AgentID = %q, want bot-2", tk.AgentID)
	}

	// Running task: ReturnToAgent is an idempotent no-op (no state/agent change).
	before := tk.State
	beforeAgent := tk.AgentID
	if err := tk.ReturnToAgent("bot-3"); err != nil {
		t.Fatalf("ReturnToAgent (running): %v", err)
	}
	if tk.State != before {
		t.Errorf("State changed on running task: %s -> %s", before, tk.State)
	}
	if tk.AgentID != beforeAgent {
		t.Errorf("AgentID changed on running task: %q -> %q", beforeAgent, tk.AgentID)
	}

	// Terminal task: ReturnToAgent returns an error.
	tk2 := NewTask("code", "y")
	if err := tk2.Start("bot-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tk2.Complete("done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := tk2.ReturnToAgent("bot-2"); err == nil {
		t.Fatalf("ReturnToAgent on terminal task: want error, got nil")
	}
}

// TestPauseResumeCancelAcrossStates verifies Cancel/Pause/Resume behave across
// a representative set of non-terminal states where the full Pause → BLOCKED →
// Resume → PriorState cycle is legal (per validTransitions).
func TestPauseResumeCancelAcrossStates(t *testing.T) {
	// States where Transition(...) has entries (non-terminal), and where the
	// full pause/resume cycle is legal: S → BLOCKED and BLOCKED → S.
	for _, st := range []domain.TaskState{
		domain.TaskAnalyzing,
		domain.TaskPlanning,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskBlocked,
	} {
		t.Run(string(st), func(t *testing.T) {
			tk := NewTask("code", "x")
			tk.State = st
			if st == domain.TaskBlocked {
				tk.PriorState = domain.TaskAnalyzing
			}

			// Pause → BLOCKED with PriorState recorded.
			if err := tk.Pause("pause"); err != nil {
				t.Fatalf("Pause from %s: %v", st, err)
			}
			if tk.State != domain.TaskBlocked {
				t.Fatalf("after Pause State = %s, want BLOCKED", tk.State)
			}
			if tk.PriorState != st && st != domain.TaskBlocked {
				t.Fatalf("PriorState = %s, want %s", tk.PriorState, st)
			}

			// Resume → back to PriorState (or ANALYZING if prior was BLOCKED).
			if err := tk.Resume(); err != nil {
				t.Fatalf("Resume from BLOCKED: %v", err)
			}
			want := st
			if st == domain.TaskBlocked {
				want = domain.TaskAnalyzing
			}
			if tk.State != want {
				t.Fatalf("after Resume State = %s, want %s", tk.State, want)
			}

			// Cancel succeeds from the (now resumed) non-terminal state.
			if err := tk.Cancel("cancel"); err != nil {
				t.Fatalf("Cancel from %s: %v", tk.State, err)
			}
			if tk.State != domain.TaskCancelled {
				t.Fatalf("after Cancel State = %s, want CANCELLED", tk.State)
			}
		})
	}
}

// TestSnapshotRecordsRichFields verifies SnapshotStore.Record persists the
// rich Goal field, surfaced via History.
func TestSnapshotRecordsRichFields(t *testing.T) {
	store := NewSnapshotStore(t.TempDir())
	tk := NewTask("code", "x")
	tk.Intent = "my rich intent"
	if err := store.Record(*tk); err != nil {
		t.Fatalf("Record: %v", err)
	}
	history, err := store.History(tk.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Goal != "my rich intent" {
		t.Errorf("History()[0].Goal = %q, want my rich intent", history[0].Goal)
	}
}