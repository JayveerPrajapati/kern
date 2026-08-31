package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// ErrInvalidTransition is returned by Task.Transition when the requested
// state change is not permitted by the task state machine. Callers can use
// errors.Is to distinguish an illegal state change from other failures (e.g.
// an HTTP layer mapping it to 409 Conflict instead of 500).
var ErrInvalidTransition = errors.New("agent: invalid task transition")

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

	// ResumeStep is the workflow step index at which an approval-gated run was
	// parked. It is persisted with the task so a fresh engine can
	// resume the same run after the human resolves the gate — the run is the
	// task, not a process-local engine.
	ResumeStep int
	// ApprovalRefs maps a pending approval ID to the workflow step index it
	// gates, persisted so the gate can be re-checked against a persistent
	// approval store on resume (e.g. `kern approve` writes the store).
	ApprovalRefs map[string]int

	// Aggregate references ( Task Center). These are additive identity
	// fields tying the Task to the project/workspace it operates on, who
	// requested it, and the *ref IDs of related artifacts (memory, policy,
	// approval, deployment, outcome). They persist through TaskStore because
	// it JSON-marshals the whole Task.
	Project       string // project/workspace name
	Repository    string // repository name
	Scope         string // task scope: "repository", "service", "package", "file", etc.
	Requester     string // who requested (agent id or "human")
	MemoryRef     string // pointer/object id reference to the task's memory
	PolicyRef     string // policy evaluation id
	ApprovalRef   string // approval id
	DeploymentRef string // deployment id
	OutcomeRef    string // outcome/observation id

	// Plural aggregate refs ( Task Center). These are additive identity
	// fields that supplement the singular refs above with *sets* of related
	// artifact refs and the lifecycle output ref IDs (context, impact, risk,
	// plan, verification, PR). The existing singular fields and inline structs
	// are kept unchanged for backward compatibility. Like the singular refs,
	// these persist through TaskStore because it JSON-marshals the whole Task.
	AgentRefs       []string // refs to the agents involved (distinct from singular AgentID)
	MemoryRefs      []string // plural refs to related memory artifacts
	ContextRef      string   // ref id of the assembled context packet
	ImpactRef       string   // ref id of the impact report
	RiskRef         string   // ref id of the risk assessment
	PlanRef         string   // ref id of the implementation plan
	VerificationRef string   // ref id of the verification result
	PRRef           string   // ref id of the pull request
	LearningRef     string   // ref id of the learning/lesson record produced by this task

	// CurrentStage is the workflow stage the task is on (e.g. "analyze",
	// "plan", "code", "verify"). It is a finer-grained position than State:
	// State is the state machine vertex, CurrentStage is where the workflow
	// engine is inside it. Persisted with the task .
	CurrentStage string

	// Retry tracking. Retry only reopens safe/idempotent actions;
	// these fields record the attempt, reason, and previous result so the
	// retry trail is auditable.
	RetryCount  int    // number of retries performed
	RetryReason string // reason for the last retry
	LastResult  string // result (Output) before the last retry

	// stateMu serializes state transitions on a shared *Task so concurrent
	// callers (the workflow engine stepping a task while a cancel/pause
	// handler runs against the same pointer) cannot interleave Transition's
	// read-modify-write. It is a POINTER so value copies made by
	// TaskStore.Save(*tk) share the same lock instead of tripping go vet's
	// copylocks check. RWMutex so Snapshot() readers can share the lock.
	// Unexported: never JSON-marshaled.
	stateMu *sync.RWMutex

	// Structured outputs produced during execution (the agent result contract).
	Evidence          []domain.Claim // evidence-backed claims produced by this task
	Risks             []domain.Risk  // risks identified during this task
	Confidence        float64        // 0.0-1.0 confidence in the task outcome
	RecommendedAction string         // "continue", "retry", "escalate", "abort", etc.

	// Lifecycle results — the
	// requires a Task to reference its full lifecycle output so it is the
	// authoritative object for audit/resume/debugging. These are populated by
	// TaskService as the task progresses through analyze → impact → plan →
	// verify. They are intentionally *domain types* (not engine types) so the
	// Task stays decoupled from engine internals.
	ContextPacket *domain.ContextPacket            // assembled context for the change
	ImpactReport  *whatif.Impact                   // deterministic impact (what-if/simulate)
	Impact        *domain.ImpactReport             // 11-question deterministic impact
	Plan          *domain.Plan                     // structured implementation plan
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
// The full code-change lifecycle follows the
// 20-step vertical slice: CREATED → ANALYZING → PLANNING → WAITING_FOR_APPROVAL
// → APPROVED → EXECUTING → VERIFYING → READY_FOR_PR → PR_CREATED → DEPLOYING →
// OBSERVING → COMPLETED. Read-only tasks (analyze, verify) may complete
// directly from their respective state without driving the full lifecycle.
var validTransitions = map[domain.TaskState][]domain.TaskState{
	domain.TaskCreated:         {domain.TaskAnalyzing, domain.TaskVerifying, domain.TaskExecuting, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskAnalyzing:       {domain.TaskPlanning, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskPlanning:        {domain.TaskWaitingApproval, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskWaitingApproval: {domain.TaskApproved, domain.TaskRejected, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskApproved:        {domain.TaskExecuting, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled, domain.TaskRejected},
	domain.TaskExecuting:       {domain.TaskVerifying, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskVerifying:       {domain.TaskReadyForPR, domain.TaskPRCreated, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled},
	domain.TaskReadyForPR:      {domain.TaskPRCreated, domain.TaskFailed, domain.TaskCancelled},
	domain.TaskPRCreated:       {domain.TaskDeploying, domain.TaskCompleted, domain.TaskFailed, domain.TaskRolledBack},
	domain.TaskDeploying:       {domain.TaskObserving, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
	domain.TaskObserving:       {domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskRolledBack},
	// Recoverable states: Retry reopens FAILED → ANALYZING; Resume reopens
	// BLOCKED → PriorState (or any valid runnable state). BLOCKED must be able
	// to resume to every state it can legally be paused/blocked from, or
	// Pause/Block/HumanTakeover during approval, deploy or observe would make
	// the task unresumable.
	domain.TaskFailed: {domain.TaskAnalyzing},
	domain.TaskBlocked: {
		domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval,
		domain.TaskApproved, domain.TaskExecuting, domain.TaskVerifying,
		domain.TaskDeploying, domain.TaskObserving,
		domain.TaskCompleted, domain.TaskFailed, domain.TaskCancelled,
	},
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
		stateMu: &sync.RWMutex{},
	}
}

// stateLock returns the task's transition mutex, lazily initializing it for
// tasks unmarshaled from JSON (which bypass NewTask and so have a nil stateMu).
// Two copies of the same persisted task each get their own mutex, so
// cross-copy concurrency is NOT serialized here — callers that share a Task
// pointer (registry + workflow engine) share the mutex and are safe; callers
// that work on freshly-loaded copies must serialize at their own layer (the
// store's path lock).
func (t *Task) stateLock() *sync.RWMutex {
	if t.stateMu == nil {
		t.stateMu = &sync.RWMutex{}
	}
	return t.stateMu
}

// Terminal reports whether the task has reached a truly final state, under
// the state lock. Use this from concurrent callers (workflow engine stepping
// a task that a cancel/pause handler may also mutate) instead of the embedded
// domain.Task.IsTerminal, which reads State without the lock.
func (t *Task) Terminal() bool {
	t.stateLock().RLock()
	defer t.stateLock().RUnlock()
	return t.IsTerminal()
}

// Validate checks the Task aggregate invariants: required fields, valid
// state, valid references. It returns a descriptive error for the
// first violation. Refs are opaque identifiers — they are validated for being
// non-empty when set, not for resolvability (that is the store's job).
func (t *Task) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("task: missing required field id")
	}
	if t.Type == "" {
		return fmt.Errorf("task %s: missing required field type", t.ID)
	}
	if t.State == "" {
		return fmt.Errorf("task %s: missing required field state", t.ID)
	}
	if !validTaskState(t.State) {
		return fmt.Errorf("task %s: invalid state %q", t.ID, t.State)
	}
	if t.CreatedAt.IsZero() {
		return fmt.Errorf("task %s: missing required field created_at", t.ID)
	}
	// Refs: empty is fine (not yet produced), but a non-empty ref must be a
	// usable identifier — reject whitespace-only refs as invalid.
	refs := []struct {
		name string
		val  string
	}{
		{"context_ref", t.ContextRef}, {"impact_ref", t.ImpactRef},
		{"risk_ref", t.RiskRef}, {"policy_ref", t.PolicyRef},
		{"approval_ref", t.ApprovalRef}, {"plan_ref", t.PlanRef},
		{"verification_ref", t.VerificationRef}, {"pr_ref", t.PRRef},
		{"deployment_ref", t.DeploymentRef}, {"outcome_ref", t.OutcomeRef},
		{"learning_ref", t.LearningRef}, {"memory_ref", t.MemoryRef},
	}
	for _, r := range refs {
		if strings.TrimSpace(r.val) == "" && r.val != "" {
			return fmt.Errorf("task %s: invalid reference %s: whitespace-only", t.ID, r.name)
		}
	}
	return nil
}

// validTaskState reports whether s is one of the 17 canonical task states.
func validTaskState(s domain.TaskState) bool {
	switch s {
	case domain.TaskCreated, domain.TaskAnalyzing, domain.TaskPlanning,
		domain.TaskWaitingApproval, domain.TaskApproved, domain.TaskExecuting,
		domain.TaskVerifying, domain.TaskReadyForPR, domain.TaskPRCreated,
		domain.TaskDeploying, domain.TaskObserving, domain.TaskCompleted,
		domain.TaskFailed, domain.TaskBlocked, domain.TaskRejected,
		domain.TaskCancelled, domain.TaskRolledBack:
		return true
	}
	return false
}

// Start marks the task in progress (ANALYZING) and binds it to the given agent.
func (t *Task) Start(agentID string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if err := t.transitionUnlocked(domain.TaskAnalyzing); err != nil {
		return err
	}
	t.AgentID = agentID
	return nil
}

// Complete marks the task completed with the given output. It fails if the
// task is already terminal (fail closed) or if the current state does not
// legally transition to COMPLETED.
func (t *Task) Complete(output string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot complete", t.ID, t.State)
	}
	if err := t.transitionUnlocked(domain.TaskCompleted); err != nil {
		return err
	}
	t.Output = output
	return nil
}

// Fail marks the task failed with an error message. It fails if already
// terminal or if the current state does not legally transition to FAILED.
func (t *Task) Fail(errMsg string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot fail", t.ID, t.State)
	}
	if err := t.transitionUnlocked(domain.TaskFailed); err != nil {
		return err
	}
	t.Output = errMsg
	return nil
}

// Transition validates and applies a state change. It is safe for concurrent
// callers sharing the same *Task : the
// read-modify-write of State is serialized by the task's state mutex, so two
// racing transitions each validate against the current state and only legal
// ones apply.
func (t *Task) Transition(next domain.TaskState) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	return t.transitionUnlocked(next)
}

// transitionUnlocked is the lock-free core of Transition. Callers must hold
// t.stateLock() (compound mutators lock once around their whole
// read-modify-write, then call this).
func (t *Task) transitionUnlocked(next domain.TaskState) error {
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
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.State, next)
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
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot cancel", t.ID, t.State)
	}
	if err := t.transitionUnlocked(domain.TaskCancelled); err != nil {
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
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot timeout", t.ID, t.State)
	}
	if err := t.transitionUnlocked(domain.TaskFailed); err != nil {
		return err
	}
	t.Output = "task timed out"
	return nil
}

// Block transitions the task to BLOCKED, recording its PriorState so Resume
// can return to it. Fails if already terminal.
func (t *Task) Block(reason string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot block", t.ID, t.State)
	}
	prior := t.State
	if err := t.transitionUnlocked(domain.TaskBlocked); err != nil {
		return err
	}
	t.PriorState = prior
	if reason != "" {
		t.Output = reason
	}
	return nil
}

// Pause transitions the task to BLOCKED, recording its PriorState so Resume
// can return to it. It is the dedicated pause primitive : like
// Block, it requires the task to be non-terminal. Idempotent: if the task is
// already BLOCKED, Pause is a no-op.
func (t *Task) Pause(reason string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot pause", t.ID, t.State)
	}
	if t.State == domain.TaskBlocked {
		return nil // idempotent: already paused
	}
	prior := t.State
	if err := t.transitionUnlocked(domain.TaskBlocked); err != nil {
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
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
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
	return t.transitionUnlocked(target)
}

// Retry reopens a FAILED task back to ANALYZING for re-execution. Idempotent:
// if the task is already non-terminal, Retry is a no-op. Fails if the task is
// truly-terminal (COMPLETED, CANCELLED, REJECTED, ROLLED_BACK).
func (t *Task) Retry() error {
	return t.RetryWithReason("retry requested")
}

// RetryWithReason reopens a FAILED task back to ANALYZING, tracking the retry
// attempt, reason, and the previous result (LastResult) so the retry trail is
// auditable. Idempotent: if the task is already non-terminal, it is a no-op.
// Fails if the task is truly-terminal.
func (t *Task) RetryWithReason(reason string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot retry", t.ID, t.State)
	}
	if t.State != domain.TaskFailed {
		return nil // idempotent: already running, nothing to retry
	}
	t.LastResult = t.Output
	if err := t.transitionUnlocked(domain.TaskAnalyzing); err != nil {
		return err
	}
	t.RetryCount++
	if reason == "" {
		reason = "retry requested"
	}
	t.RetryReason = reason
	return nil
}

// Rollback transitions a PR_CREATED / DEPLOYING / OBSERVING task to
// ROLLED_BACK with a reason. Fails if the current state does not legally
// transition to ROLLED_BACK.
func (t *Task) Rollback(reason string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if err := t.transitionUnlocked(domain.TaskRolledBack); err != nil {
		return err
	}
	if reason != "" {
		t.Output = reason
	}
	return nil
}

// HumanTakeover blocks the task and binds it to a human agent. It records the
// prior state so the task can be resumed after human intervention. Behavior
// mirrors the old Block("human takeover") + rebind: the reason is recorded in
// Output.
func (t *Task) HumanTakeover(agentID string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot take over", t.ID, t.State)
	}
	prior := t.State
	if err := t.transitionUnlocked(domain.TaskBlocked); err != nil {
		return err
	}
	t.PriorState = prior
	t.Output = "human takeover"
	t.AgentID = agentID
	return nil
}

// ReturnToAgent hands a human-takeover (BLOCKED) task back to an agent. It
// resumes the task to its PriorState (via Resume) and reassigns AgentID to the
// given agent. Semantics mirror Resume:
// - BLOCKED (paused / human-takeover): resumed to PriorState and rebound.
// - already running (non-BLOCKED, non-terminal): idempotent no-op.
// - truly-terminal: returns an error.
func (t *Task) ReturnToAgent(agentID string) error {
	t.stateLock().Lock()
	defer t.stateLock().Unlock()
	if t.IsTerminal() {
		return fmt.Errorf("task %s: already in terminal state %s; cannot return to agent", t.ID, t.State)
	}
	if t.State != domain.TaskBlocked {
		return nil // idempotent no-op: already running, nothing to return
	}
	if err := t.transitionUnlocked(t.resumeTarget()); err != nil {
		return err
	}
	t.AgentID = agentID
	return nil
}

// resumeTarget is the state a BLOCKED task resumes to: PriorState, or
// ANALYZING when PriorState is empty/BLOCKED. Callers must hold the lock.
func (t *Task) resumeTarget() domain.TaskState {
	target := t.PriorState
	if target == "" || target == domain.TaskBlocked {
		target = domain.TaskAnalyzing
	}
	return target
}

// Snapshot returns a compact domain.ContextSnapshot of the task for
// resume/replay. It derives the Goal from Intent (falling back to Input) and
// the State from the task's current state. Risks are reduced to their Level
// string (or their String() representation if one exists).
func (t *Task) Snapshot() domain.ContextSnapshot {
	t.stateLock().RLock()
	defer t.stateLock().RUnlock()
	goal := t.Intent
	if goal == "" {
		goal = t.Input
	}
	risks := make([]string, 0, len(t.Risks))
	for _, r := range t.Risks {
		if s, ok := interface{}(r).(fmt.Stringer); ok {
			risks = append(risks, s.String())
		} else {
			risks = append(risks, string(r.Level))
		}
	}
	return domain.ContextSnapshot{
		Goal:        goal,
		State:       string(t.State),
		Decisions:   []string{},
		Constraints: []string{},
		Files:       []string{},
		Tests:       []string{},
		Risks:       risks,
		NextAction:  "",
	}
}
