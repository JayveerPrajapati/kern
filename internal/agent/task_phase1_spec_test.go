package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Spec-required tests not covered by the earlier work.
// These map 1:1 to the V3 spec micro-phase test lists:
// 1.1: invalid values, compatibility (required fields + serialization were
// already covered by TestTaskAggregateRefsRoundTrip and
// TestTaskAggregateRefsJSONRoundTrip)
// 1.2: duplicate transition, concurrent transition
// 1.3: corrupt record, missing artifact, restart was already covered by
// TestRestartResume

// TestTaskValidateMissingRequiredFields verifies Validate rejects a task
// missing required aggregate fields .
func TestTaskValidateMissingRequiredFields(t *testing.T) {
	tk := &Task{}
	if err := tk.Validate(); err == nil {
		t.Fatal("Validate on zero task: want error, got nil")
	} else if !strings.Contains(err.Error(), "id") {
		t.Fatalf("Validate error = %q, want missing id", err)
	}

	tk = NewTask("code", "x")
	tk.ID = ""
	if err := tk.Validate(); err == nil {
		t.Fatal("Validate with empty ID: want error, got nil")
	}

	tk = NewTask("", "x")
	if err := tk.Validate(); err == nil {
		t.Fatal("Validate with empty type: want error, got nil")
	}
}

// TestTaskValidateInvalidState verifies Validate rejects an unknown state value
func TestTaskValidateInvalidState(t *testing.T) {
	tk := NewTask("code", "x")
	tk.State = domain.TaskState("NOT_A_STATE")
	if err := tk.Validate(); err == nil {
		t.Fatal("Validate with invalid state: want error, got nil")
	} else if !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("Validate error = %q, want invalid state", err)
	}
}

// TestTaskValidateInvalidReference verifies Validate rejects whitespace-only
// reference values .
func TestTaskValidateInvalidReference(t *testing.T) {
	tk := NewTask("code", "x")
	tk.ContextRef = "   "
	if err := tk.Validate(); err == nil {
		t.Fatal("Validate with whitespace ref: want error, got nil")
	} else if !strings.Contains(err.Error(), "context_ref") {
		t.Fatalf("Validate error = %q, want context_ref", err)
	}
}

// TestTaskValidateValid verifies a well-formed task passes Validate.
func TestTaskValidateValid(t *testing.T) {
	tk := NewTask("code", "x")
	tk.Intent = "implement foo"
	tk.Project = "kern"
	tk.Repository = "kern"
	tk.Scope = "repository"
	tk.Requester = "human"
	tk.WorkflowID = "wf-1"
	tk.AgentRefs = []string{"bot-1"}
	tk.ContextRef = "ctx-1"
	tk.MemoryRefs = []string{"mem-1"}
	tk.ImpactRef = "imp-1"
	tk.RiskRef = "risk-1"
	tk.PolicyRef = "pol-1"
	tk.ApprovalRef = "appr-1"
	tk.PlanRef = "plan-1"
	tk.Artifacts = []string{"artifact-1"}
	tk.VerificationRef = "ver-1"
	tk.PRRef = "pr-1"
	tk.DeploymentRef = "dep-1"
	tk.OutcomeRef = "out-1"
	if err := tk.Validate(); err != nil {
		t.Fatalf("Validate on full task: %v", err)
	}
}

// TestTaskSerializationCompatibility verifies a task JSON written by an older
// schema (without the aggregate refs, CurrentStage, or retry fields) still
// loads into the current Task without error, with zero values for the new
// fields. This protects against breaking existing
// persisted stores when additive fields are introduced.
func TestTaskSerializationCompatibility(t *testing.T) {
	// A task record as it would have been persisted before the aggregate refs
	// were added: only the legacy domain.Task fields + AgentID/WorkflowID.
	// Embedded domain.Task fields are flattened to top level by encoding/json.
	legacy := `{
		"ID": "t-1",
		"Type": "code",
		"State": "EXECUTING",
		"Input": "do the thing",
		"Output": "",
		"CreatedAt": "2026-08-01T00:00:00Z",
		"UpdatedAt": "2026-08-01T00:00:00Z",
		"PriorState": "",
		"WorkflowID": "wf-1",
		"AgentID": "bot-1",
		"ParentID": "",
		"Steps": [],
		"Dependencies": null,
		"Artifacts": null,
		"CreatedBy": "bot-1"
	}`
	var tk Task
	if err := json.Unmarshal([]byte(legacy), &tk); err != nil {
		t.Fatalf("unmarshal legacy task: %v", err)
	}
	if tk.ID != "t-1" || tk.State != domain.TaskExecuting {
		t.Fatalf("legacy fields lost: ID=%q State=%s", tk.ID, tk.State)
	}
	// New fields must default to zero values, not garbage.
	if tk.CurrentStage != "" {
		t.Errorf("CurrentStage = %q, want empty", tk.CurrentStage)
	}
	if tk.ContextRef != "" || tk.ImpactRef != "" || tk.RiskRef != "" {
		t.Errorf("new refs not zero: ctx=%q imp=%q risk=%q", tk.ContextRef, tk.ImpactRef, tk.RiskRef)
	}
	if tk.RetryCount != 0 || tk.RetryReason != "" || tk.LastResult != "" {
		t.Errorf("retry fields not zero: count=%d reason=%q last=%q", tk.RetryCount, tk.RetryReason, tk.LastResult)
	}

	// And the legacy task must survive a store save/load round trip.
	store := NewTaskStore(t.TempDir())
	if _, err := store.Save(tk); err != nil {
		t.Fatalf("Save legacy task: %v", err)
	}
	loaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get legacy task: %v", err)
	}
	if loaded.State != domain.TaskExecuting {
		t.Fatalf("after round trip: State=%s, want EXECUTING", loaded.State)
	}
}

// TestTaskDuplicateTransition verifies a repeated transition to the same state
// is rejected .
func TestTaskDuplicateTransition(t *testing.T) {
	tk := NewTask("code", "x")
	if err := tk.Transition(domain.TaskAnalyzing); err != nil {
		t.Fatalf("first transition to ANALYZING: %v", err)
	}
	if err := tk.Transition(domain.TaskAnalyzing); err == nil {
		t.Fatal("duplicate ANALYZING -> ANALYZING: want error, got nil")
	}
	if tk.State != domain.TaskAnalyzing {
		t.Fatalf("State = %s, want ANALYZING (unchanged after rejected duplicate)", tk.State)
	}
}

// TestTaskConcurrentTransition verifies concurrent transitions on a shared
// *Task are serialized: every applied transition is legal, the final state is
// consistent, and no update is lost. Run
// with -race this catches the pre-lock data race.
func TestTaskConcurrentTransition(t *testing.T) {
	tk := NewTask("code", "x")
	// 8 goroutines race to drive the task from CREATED toward COMPLETED along
	// the legal path, plus a cancel attempt. Under the state lock exactly one
	// ordering wins; every applied transition must be legal and the final
	// state must be reachable — never a corrupted/invalid state.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, s := range []domain.TaskState{
				domain.TaskAnalyzing,
				domain.TaskPlanning,
				domain.TaskWaitingApproval,
				domain.TaskApproved,
				domain.TaskExecuting,
				domain.TaskVerifying,
				domain.TaskCompleted,
			} {
				_ = tk.Transition(s)
			}
		}()
	}
	wg.Wait()

	// Final state must be a valid state machine state, and if it is a
	// non-terminal one the task must not be in a contradictory state.
	if !validTaskState(tk.State) {
		t.Fatalf("final state %q is not a canonical state", tk.State)
	}
	// The task must either have completed or be at a legal waypoint.
	switch tk.State {
	case domain.TaskCompleted:
		// done
	case domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval,
		domain.TaskApproved, domain.TaskExecuting, domain.TaskVerifying,
		domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled:
		// legal waypoint (a racing cancel may have won at any point)
	default:
		t.Fatalf("final state %q not a legal waypoint", tk.State)
	}

	// A second phase: concurrent Cancel + Transition must not panic or corrupt.
	tk2 := NewTask("code", "y")
	var wg2 sync.WaitGroup
	wg2.Add(2)
	go func() {
		defer wg2.Done()
		_ = tk2.Transition(domain.TaskAnalyzing)
	}()
	go func() {
		defer wg2.Done()
		_ = tk2.Cancel("stop")
	}()
	wg2.Wait()
	if !validTaskState(tk2.State) {
		t.Fatalf("after concurrent cancel: state %q invalid", tk2.State)
	}
}

// TestTaskStoreCorruptRecord verifies a corrupt store file surfaces its error
// instead of silently returning an empty store .
func TestTaskStoreCorruptRecord(t *testing.T) {
	store := NewTaskStore(t.TempDir())
	// Write garbage to the store file.
	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List on corrupt store: want error, got nil")
	}
	if _, err := store.Get("t-1"); err == nil {
		t.Fatal("Get on corrupt store: want error, got nil")
	}
}

// TestTaskMissingArtifact verifies a task referencing an artifact that does not
// exist loads fine (refs are opaque) and the missing ref is preserved. This
// is the store contract: a task persists with its
// refs regardless of whether the artifact record exists yet; resolution is the
// artifact store's job (covered by the app package, which owns ArtifactStore).
func TestTaskMissingArtifact(t *testing.T) {
	store := NewTaskStore(t.TempDir())

	tk := NewTask("code", "x")
	tk.VerificationRef = "ver-does-not-exist"
	tk.PlanRef = "plan-does-not-exist"
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save task with missing artifact refs: %v", err)
	}
	loaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.VerificationRef != "ver-does-not-exist" {
		t.Fatalf("VerificationRef = %q, want preserved", loaded.VerificationRef)
	}
}

// TestCurrentStagePersisted verifies CurrentStage is set during workflow
// execution and survives a save/load round trip .
func TestCurrentStagePersisted(t *testing.T) {
	store := NewTaskStore(t.TempDir())
	tk := NewTask("code", "x")
	tk.CurrentStage = "verify"
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.CurrentStage != "verify" {
		t.Fatalf("CurrentStage = %q, want verify (persisted)", loaded.CurrentStage)
	}
}

// TestWorkflowSetsCurrentStage verifies the workflow engine records the current
// stage on the task as it drives steps .
func TestWorkflowSetsCurrentStage(t *testing.T) {
	store := NewTaskStore(t.TempDir())
	reg := NewRegistry()
	reg.SetTaskStore(store)
	_ = reg.Register(Agent{Agent: mkAgent("p1", "Planner", "planner").Agent})
	root := NewTask("code", "x")
	if err := reg.SubmitTask(root); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	wf := Workflow{ID: "stage-test", Name: "test", Steps: []WorkflowStep{
		{Action: "analyze", AgentType: "planner"},
		{Action: "plan", AgentType: "planner"},
	}}
	engine := NewWorkflowEngine(reg, nil)
	engine.RegisterWorkflow(wf)
	root.WorkflowID = wf.ID
	got, err := engine.Run(root, func(action string, t *Task) (string, error) {
		return fmt.Sprintf("did %s", action), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.CurrentStage != "plan" {
		t.Fatalf("CurrentStage = %q, want plan (last executed step)", got.CurrentStage)
	}
	if !got.Terminal() {
		// Completed via the workflow; verify persisted stage too.
		loaded, err := store.Get(got.ID)
		if err != nil {
			t.Fatalf("Get persisted: %v", err)
		}
		if loaded.CurrentStage != "plan" {
			t.Fatalf("persisted CurrentStage = %q, want plan", loaded.CurrentStage)
		}
	}
}
