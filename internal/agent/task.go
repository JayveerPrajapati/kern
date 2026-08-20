package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
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
var validTransitions = map[domain.TaskState][]domain.TaskState{
	domain.TaskCreated:         {domain.TaskAnalyzing, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskAnalyzing:       {domain.TaskPlanning, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskPlanning:        {domain.TaskWaitingApproval, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskWaitingApproval: {domain.TaskApproved, domain.TaskRejected, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskApproved:        {domain.TaskExecuting, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled, domain.TaskRejected},
	domain.TaskExecuting:       {domain.TaskVerifying, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskVerifying:       {domain.TaskReadyForPR, domain.TaskPRCreated, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskReadyForPR:      {domain.TaskPRCreated, domain.TaskFailed, domain.TaskCancelled},
	domain.TaskPRCreated:       {domain.TaskDeploying, domain.TaskCompleted, domain.TaskFailed, domain.TaskRolledBack},
	domain.TaskDeploying:       {domain.TaskObserving, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
	domain.TaskObserving:       {domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
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
