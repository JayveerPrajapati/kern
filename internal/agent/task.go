package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Task is the runtime task model. It extends domain.Task with execution state
// tracked by the runtime.
type Task struct {
	domain.Task
	WorkflowID   string   // which workflow this task belongs to
	AgentID      string   // agent currently executing
	ParentID     string   // task that spawned this one (empty = root)
	Steps        []Step   // execution history
	Dependencies []string // task IDs that must complete first
	Artifacts    []string // artifact paths produced
	CreatedBy    string   // agent that created this task

	// Structured outputs produced during execution (the agent result contract).
	Evidence          []domain.Claim // evidence-backed claims produced by this task
	Risks             []domain.Risk  // risks identified during this task
	Confidence        float64        // 0.0-1.0 confidence in the task outcome
	RecommendedAction string         // "continue", "retry", "escalate", "abort", etc.

	// Lifecycle results — the Integration Transformation Plan's Phase 2
	// requires a Task to reference its full lifecycle output so it is the
	// authoritative object for audit/resume/debugging. These are populated by
	// TaskService as the task progresses through analyze → impact → plan →
	// verify. They are intentionally *domain types* (not engine types) so the
	// Task stays decoupled from engine internals.
	ContextPacket *domain.ContextPacket            // assembled context for the change
	ImpactReport  *whatif.Impact                   // deterministic impact (what-if/simulate)
	Impact        *domain.ImpactReport             // 11-question deterministic impact (Phase 7)
	Plan          *domain.Plan                     // structured implementation plan (Phase 6)
	Verification  *verification.VerificationResult // last verification run
	Intent        string                           // the original human request (from Input)

	PRURL    string // URL of created PR (empty if noop/failed)
	PRNumber int    // PR number (0 if noop/failed)
}

// Step records one execution step of a task.
type Step struct {
	Index      int       // 1-based ordinal within the task's step history
	Action     string    // "analyze", "plan", "code", "verify", ...
	AgentID    string    // agent that performed the step
	StartedAt  time.Time // zero when the step has not started
	FinishedAt time.Time // zero when the step has not finished
	Result     string    // short summary or output
	Status     string    // "success", "failed", "blocked", "skipped"
}

// taskSeq is the package-level counter for deterministic task IDs ("t-<n>").
var taskSeq struct {
	sync.Mutex
	n int
}

func nextTaskID() string {
	taskSeq.Lock()
	defer taskSeq.Unlock()
	taskSeq.n++
	return fmt.Sprintf("t-%d", taskSeq.n)
}

// validTransitions encodes the TaskState state machine. Terminal states are not
// keys, so no transition is possible FROM them (fail closed).
//
// The full code-change lifecycle follows the Integration Transformation Plan's
// 20-step vertical slice: CREATED → ANALYZING → PLANNING → WAITING_FOR_APPROVAL
// → APPROVED → EXECUTING → VERIFYING → READY_FOR_PR → PR_CREATED → DEPLOYING →
// OBSERVING → COMPLETED. Read-only tasks (analyze, verify) may complete
// directly from their respective state without driving the full lifecycle.
var validTransitions = map[domain.TaskState][]domain.TaskState{
	domain.TaskCreated:         {domain.TaskAnalyzing, domain.TaskVerifying, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskAnalyzing:       {domain.TaskPlanning, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskPlanning:        {domain.TaskWaitingApproval, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskWaitingApproval: {domain.TaskApproved, domain.TaskRejected, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskApproved:        {domain.TaskExecuting, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled, domain.TaskRejected},
	domain.TaskExecuting:       {domain.TaskVerifying, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskVerifying:       {domain.TaskReadyForPR, domain.TaskPRCreated, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskReadyForPR:      {domain.TaskPRCreated, domain.TaskFailed, domain.TaskCancelled},
	domain.TaskPRCreated:       {domain.TaskDeploying, domain.TaskCompleted, domain.TaskFailed, domain.TaskRolledBack},
	domain.TaskDeploying:       {domain.TaskObserving, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
	domain.TaskObserving:       {domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
	// Recoverable states: Retry reopens FAILED → ANALYZING; Resume reopens
	// BLOCKED → PriorState (or any valid runnable state).
	domain.TaskFailed:  {domain.TaskAnalyzing},
	domain.TaskBlocked: {domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskExecuting, domain.TaskVerifying, domain.TaskCompleted, domain.TaskFailed, domain.TaskCancelled},
}

// NewTask creates a new task with a generated ID, starting in the CREATED state.
func NewTask(taskType, input string) *Task {
	now := time.Now()
	return &Task{
		Task: domain.Task{
			ID:        nextTaskID(),
			Type:      taskType,
			State:     domain.TaskCreated,
			Input:     input,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

// Start marks the task in progress (ANALYZING) and binds it to the given agent.
func (t *Task) Start(agentID string) error {
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		return err
	}
	t.AgentID = agentID
	return nil
}

// Complete marks the task completed with the given output. It fails if the
// task is already terminal (fail closed) or if the current state does not
// legally transition to COMPLETED.
func (t *Task) Complete(output string) error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot complete", t.ID, t.State)
	}
	if err := t.Transition(domain.TaskCompleted); err != nil {
		return err
	}
	t.Output = output
	return nil
}

// Fail marks the task failed with an error message. It fails if already
// terminal or if the current state does not legally transition to FAILED.
func (t *Task) Fail(errMsg string) error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot fail", t.ID, t.State)
	}
	if err := t.Transition(domain.TaskFailed); err != nil {
		return err
	}
	t.Output = errMsg
	return nil
}

// Transition validates and applies a state change.
func (t *Task) Transition(next domain.TaskState) error {
	allowed, ok := validTransitions[t.State]
	if !ok {
		return fmt.Errorf("task %s: state %q is terminal or unknown; no transition allowed", t.ID, t.State)
	}
	for _, s := range allowed {
		if s == next {
			t.State = next
			t.UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("task %s: invalid transition %s -> %s", t.ID, t.State, next)
}

// AddStep appends an execution step, assigning its 1-based index.
func (t *Task) AddStep(step Step) {
	step.Index = len(t.Steps) + 1
	t.Steps = append(t.Steps, step)
}

// Cancel transitions the task to CANCELLED with a reason. Fails if the task
// is already in a truly-terminal state (COMPLETED, CANCELLED, REJECTED,
// ROLLED_BACK) or if the current state does not legally transition to
// CANCELLED.
func (t *Task) Cancel(reason string) error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot cancel", t.ID, t.State)
	}
	if err := t.Transition(domain.TaskCancelled); err != nil {
		return err
	}
	if reason != "" {
		t.Output = reason
	}
	return nil
}

// Timeout transitions the task to FAILED, indicating it exceeded a deadline.
// Fails if already terminal.
func (t *Task) Timeout() error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot timeout", t.ID, t.State)
	}
	if err := t.Transition(domain.TaskFailed); err != nil {
		return err
	}
	t.Output = "task timed out"
	return nil
}

// Block transitions the task to BLOCKED, recording its PriorState so Resume
// can return to it. Fails if already terminal.
func (t *Task) Block(reason string) error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot block", t.ID, t.State)
	}
	prior := t.State
	if err := t.Transition(domain.TaskBlocked); err != nil {
		return err
	}
	t.PriorState = prior
	if reason != "" {
		t.Output = reason
	}
	return nil
}

// Resume transitions a BLOCKED task back to its PriorState (or ANALYZING if
// PriorState is empty). Idempotent: if the task is already non-terminal and
// not BLOCKED, Resume is a no-op. Fails if the task is truly-terminal.
func (t *Task) Resume() error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot resume", t.ID, t.State)
	}
	if t.State != domain.TaskBlocked {
		return nil // idempotent: already running, nothing to resume
	}
	target := t.PriorState
	if target == "" || target == domain.TaskBlocked {
		target = domain.TaskAnalyzing
	}
	return t.Transition(target)
}

// Retry reopens a FAILED task back to ANALYZING for re-execution. Idempotent:
// if the task is already non-terminal, Retry is a no-op. Fails if the task is
// truly-terminal (COMPLETED, CANCELLED, REJECTED, ROLLED_BACK).
func (t *Task) Retry() error {
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot retry", t.ID, t.State)
	}
	if t.State != domain.TaskFailed {
		return nil // idempotent: already running, nothing to retry
	}
	return t.Transition(domain.TaskAnalyzing)
}

// Rollback transitions a PR_CREATED / DEPLOYING / OBSERVING task to
// ROLLED_BACK with a reason. Fails if the current state does not legally
// transition to ROLLED_BACK.
func (t *Task) Rollback(reason string) error {
	if err := t.Transition(domain.TaskRolledBack); err != nil {
		return err
	}
	if reason != "" {
		t.Output = reason
	}
	return nil
}

// HumanTakeover blocks the task and binds it to a human agent. It records the
// prior state so the task can be resumed after human intervention.
func (t *Task) HumanTakeover(agentID string) error {
	if err := t.Block("human takeover"); err != nil {
		return err
	}
	t.AgentID = agentID
	return nil
}
