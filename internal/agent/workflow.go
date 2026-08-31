package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// maxNormalizedStepOutput is the threshold above which a step's raw output is
// normalized : the step history keeps a compact summary while the
// raw output remains on the task and in the artifact chain.
const maxNormalizedStepOutput = 4 << 10 // 4 KiB

// ErrApprovalRequired is returned by Run when a step requires human approval
// that has not been granted. The caller must call CompleteApproval and re-run.
var ErrApprovalRequired = errors.New("agent: human approval required")

// ApprovalStore is the persistent approval backend the engine uses so an
// approval-gated run can be resumed across processes (e.g. `kern approve`
// writes the same store). A nil store keeps the approval workflow purely
// in-memory (v1 behavior).
type ApprovalStore interface {
	Get(approvalID string) (domain.Approval, error)
	AddPending(a domain.Approval) error
	Decide(approvalID, approver string, approved bool, reason string) (domain.Approval, error)
}

// approvalRequiredError wraps ErrApprovalRequired with the pending approval ID.
type approvalRequiredError struct {
	approvalID string
}

func (e approvalRequiredError) Error() string {
	return fmt.Sprintf("%v: %s", ErrApprovalRequired, e.approvalID)
}
func (e approvalRequiredError) Unwrap() error { return ErrApprovalRequired }

// ApprovalID extracts the pending approval ID from an approval-required error.
func ApprovalID(err error) string {
	var e approvalRequiredError
	if errors.As(err, &e) {
		return e.approvalID
	}
	return ""
}

// Workflow defines the execution plan for a task type.
type Workflow struct {
	ID    string
	Name  string
	Steps []WorkflowStep // ordered
}

// WorkflowStep is one step in a workflow.
type WorkflowStep struct {
	Action           string        // "analyze", "plan", "approve", "code", "verify", "pr", "deploy", "observe"
	AgentType        string        // which agent type executes this step
	RequiresApproval bool          // human approval gate before this step
	Timeout          time.Duration // zero = no timeout
}

// taskStateForAction maps a workflow action to the task state it drives.
var taskStateForAction = map[string]domain.TaskState{
	"analyze": domain.TaskAnalyzing,
	"plan":    domain.TaskPlanning,
	"code":    domain.TaskExecuting,
	"verify":  domain.TaskVerifying,
	"pr":      domain.TaskPRCreated,
	"deploy":  domain.TaskDeploying,
	"observe": domain.TaskObserving,
	"request": domain.TaskCreated,
}

// canonicalLifecycle is the full code-change state chain (the Integration
// Transformation Plan's 20-step vertical slice). The engine advances a task
// along this chain when a workflow step targets a state beyond the current
// one, so a workflow need not spell out every intermediate state (e.g. an
// incident workflow may open with "plan" while the task starts at CREATED).
var canonicalLifecycle = []domain.TaskState{
	domain.TaskCreated,
	domain.TaskAnalyzing,
	domain.TaskPlanning,
	domain.TaskWaitingApproval,
	domain.TaskApproved,
	domain.TaskExecuting,
	domain.TaskVerifying,
	domain.TaskReadyForPR,
	domain.TaskPRCreated,
	domain.TaskDeploying,
	domain.TaskObserving,
	domain.TaskCompleted,
}

// driveToState advances the task from its current state to the target state by
// walking the canonical lifecycle through each intermediate state, applying
// every transition in order. It is a no-op when the task is already at the
// target. When the current state is not on the canonical chain (e.g. a
// recoverable BLOCKED/FAILED state), it falls back to a single direct
// transition so the strict state machine still validates the move.
func driveToState(t *Task, target domain.TaskState) error {
	if t.State == target {
		return nil
	}
	curIdx, curFound := indexInLifecycle(t.State)
	tgtIdx, tgtFound := indexInLifecycle(target)
	if curFound && tgtFound && curIdx < tgtIdx {
		for _, next := range canonicalLifecycle[curIdx+1 : tgtIdx+1] {
			if err := t.Transition(next); err != nil {
				return err
			}
		}
		return nil
	}
	return t.Transition(target)
}

// indexInLifecycle returns the position of a state on the canonical lifecycle,
// reporting whether it is present.
func indexInLifecycle(s domain.TaskState) (int, bool) {
	for i, st := range canonicalLifecycle {
		if st == s {
			return i, true
		}
	}
	return 0, false
}

// approvalKey identifies one approval gate for a task+step.
func approvalKey(taskID string, step int) string {
	return fmt.Sprintf("%s:%d", taskID, step)
}

// approval tracks an in-flight approval gate keyed by its (task, step).
type approval struct {
	key string
}

// WorkflowEngine executes a task through a workflow's steps.
type WorkflowEngine struct {
	registry  *Registry
	approvals *governance.ApprovalWorkflow
	store     ApprovalStore // persistent approval backend; nil = in-memory only
	workflows map[string]Workflow

	// mu guards the approvalRef/satisfied/progress maps, which are mutated by
	// both Run and CompleteApproval (and RejectApproval).
	mu sync.Mutex
	// approvalRef maps a governance approval ID back to the (task, step) it gates.
	approvalRef map[string]approval
	// satisfied tracks which approval gates have been granted.
	satisfied map[string]bool
	// progress records the next step index per task so a Run that returned
	// ErrApprovalRequired resumes after the gate instead of re-driving from start.
	progress map[string]int
	bus      *eventbus.Bus // optional event publisher; nil = no-op
}

// NewWorkflowEngine creates an engine; nil registry/approval workflow are
// replaced with fresh ones.
func NewWorkflowEngine(registry *Registry, approvals *governance.ApprovalWorkflow) *WorkflowEngine {
	if registry == nil {
		registry = NewRegistry()
	}
	if approvals == nil {
		approvals = governance.NewApprovalWorkflow()
	}
	return &WorkflowEngine{
		registry:    registry,
		approvals:   approvals,
		workflows:   map[string]Workflow{},
		approvalRef: map[string]approval{},
		satisfied:   map[string]bool{},
		progress:    map[string]int{},
	}
}

// WithBus attaches an optional event bus. A nil bus is a no-op.
func (e *WorkflowEngine) WithBus(b *eventbus.Bus) *WorkflowEngine {
	e.bus = b
	return e
}

// WithApprovalStore attaches a persistent approval backend so approval gates
// survive process restarts and can be resolved out-of-band (e.g. via
// `kern approve`). A nil store keeps approvals in memory (v1 behavior).
func (e *WorkflowEngine) WithApprovalStore(s ApprovalStore) *WorkflowEngine {
	e.store = s
	return e
}

// publish delivers a task-lifecycle event to the optional bus.
func (e *WorkflowEngine) publish(kind eventbus.Kind, subject string, payload map[string]string) {
	if e.bus == nil {
		return
	}
	if payload == nil {
		payload = map[string]string{}
	}
	e.bus.Publish(eventbus.Event{Kind: kind, Source: "agent", Subject: subject, Payload: payload})
}

// publishTaskState emits a task-lifecycle event for a specific task state.
func (e *WorkflowEngine) publishTaskState(kind eventbus.Kind, t *Task) {
	if t == nil {
		return
	}
	e.publish(kind, t.ID, map[string]string{"state": string(t.State)})
}

// RegisterWorkflow adds a workflow definition.
func (e *WorkflowEngine) RegisterWorkflow(wf Workflow) {
	e.workflows[wf.ID] = wf
}

// GetWorkflow retrieves a workflow by ID.
func (e *WorkflowEngine) GetWorkflow(id string) (Workflow, bool) {
	wf, ok := e.workflows[id]
	return wf, ok
}

// seedFromTask restores the engine's approval-gate state from the task's
// persisted resume fields, so a fresh engine can continue a run that
// parked at an approval gate. It is a no-op when the task carries no resume
// state (first run or run already completed).
func (e *WorkflowEngine) seedFromTask(t *Task) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.ResumeStep > 0 {
		e.progress[t.ID] = t.ResumeStep
	}
	for approvalID, stepIdx := range t.ApprovalRefs {
		// The gate is satisfied if the bound approval is already approved in
		// the persistent store (out-of-band resolution, e.g. `kern approve`).
		if e.store != nil {
			if a, err := e.store.Get(approvalID); err == nil && a.Status == "approved" {
				e.satisfied[approvalKey(t.ID, stepIdx)] = true
				continue
			}
		}
		e.approvalRef[approvalID] = approval{key: approvalKey(t.ID, stepIdx)}
	}
}

// gateSatisfied reports whether a workflow approval gate is satisfied, checking
// the engine's in-memory state first and then the persistent store bound to the
// step (a fresh engine + out-of-band approval path).
func (e *WorkflowEngine) gateSatisfied(t *Task, key string, stepIdx int) bool {
	e.mu.Lock()
	sat := e.satisfied[key]
	e.mu.Unlock()
	if sat {
		return true
	}
	if e.store == nil {
		return false
	}
	for approvalID, idx := range t.ApprovalRefs {
		if idx != stepIdx {
			continue
		}
		if a, err := e.store.Get(approvalID); err == nil && a.Status == "approved" {
			e.mu.Lock()
			e.satisfied[key] = true
			e.mu.Unlock()
			return true
		}
	}
	return false
}

// resolveWorkflow returns the workflow to run for a task, defaulting to the
// standard workflow when the task names no registered workflow.
func (e *WorkflowEngine) resolveWorkflow(id string) Workflow {
	if wf, ok := e.workflows[id]; ok {
		return wf
	}
	return DefaultWorkflow()
}

// DefaultWorkflow returns the standard workflow. The "approve" step is a
// human-approval gate and does not invoke the step handler.
func DefaultWorkflow() Workflow {
	return Workflow{
		ID:   "default",
		Name: "Standard MVP2 workflow",
		Steps: []WorkflowStep{
			{Action: "request", AgentType: "planner"},
			{Action: "analyze", AgentType: "planner"},
			{Action: "plan", AgentType: "planner"},
			{Action: "approve", AgentType: "human", RequiresApproval: true},
			{Action: "code", AgentType: "coder"},
			{Action: "verify", AgentType: "reviewer"},
			{Action: "pr", AgentType: "reviewer"},
		},
	}
}

// Run executes a task through its workflow, invoking the step handler for each
// executable step and recording a step log on the task. For every step it
// drives the task's state machine, resolves the step's agent from the registry
// (failing closed when the workflow's AgentType is not registered), and for a
// RequiresApproval step it requests approval, parks the task in
// WAITING_FOR_APPROVAL, and returns (task, ErrApprovalRequired) — the caller
// then calls CompleteApproval and re-runs. A handler error marks the task
// FAILED and returns the error.
func (e *WorkflowEngine) Run(rootTask *Task, stepHandler func(action string, task *Task) (string, error)) (*Task, error) {
	metrics.Default().RecordAgentRun()
	if rootTask == nil {
		return nil, errors.New("agent: cannot run a nil task")
	}
	if stepHandler == nil {
		return nil, errors.New("agent: cannot run with a nil step handler")
	}

	wf := e.resolveWorkflow(rootTask.WorkflowID)

	// Resume state is persisted on the task : a run that parked at an
	// approval gate records its next step index and approval bindings on the
	// task, so a FRESH engine can resume the same run after the gate is
	// resolved (the run is the task, not this engine instance). Seed the
	// engine's maps from the task, then fall back to the in-memory progress.
	e.seedFromTask(rootTask)

	e.mu.Lock()
	start := e.progress[rootTask.ID]
	if rootTask.ResumeStep > start {
		start = rootTask.ResumeStep
	}
	e.mu.Unlock()
	for i := start; i < len(wf.Steps); i++ {
		step := wf.Steps[i]

		// Abort if the task reached a terminal state mid-flight.
		if rootTask.Terminal() {
			return rootTask, fmt.Errorf("agent: task %s reached terminal state %s before step %d", rootTask.ID, rootTask.State, i)
		}

		// Track the current workflow stage on the task ("current stage" is
		// persisted with the task). Set before the step runs so a
		// failure mid-step still records where the task was.
		rootTask.CurrentStage = step.Action

		// Human approval gate.
		if step.RequiresApproval {
			key := approvalKey(rootTask.ID, i)
			sat := e.gateSatisfied(rootTask, key, i)
			if !sat {
				if err := rootTask.Transition(domain.TaskWaitingApproval); err != nil {
					return rootTask, err
				}
				req := e.approvals.Request(rootTask.ID, rootTask.CreatedBy, step.Action)
				if e.store != nil {
					_ = e.store.AddPending(req)
				}
				e.mu.Lock()
				e.approvalRef[req.ID] = approval{key: key}
				// Resume at this gate step on re-run.
				e.progress[rootTask.ID] = i
				e.mu.Unlock()
				// Persist the run state on the task so a fresh engine can
				// resume this exact gate after out-of-band approval.
				if rootTask.ApprovalRefs == nil {
					rootTask.ApprovalRefs = map[string]int{}
				}
				rootTask.ApprovalRefs[req.ID] = i
				rootTask.ResumeStep = i
				e.persist(rootTask)
				rootTask.AddStep(Step{
					Action:  step.Action,
					AgentID: "human",
					Result:  req.ID,
					Status:  "blocked",
				})
				e.publish(eventbus.TaskApprovalRequested, rootTask.ID, map[string]string{"approval": req.ID, "action": step.Action})
				e.publish(eventbus.ApprovalRequested, req.ID, map[string]string{"task": rootTask.ID, "action": step.Action})
				return rootTask, approvalRequiredError{approvalID: req.ID}
			}
			// Gate satisfied: move through APPROVED and continue.
			if rootTask.State == domain.TaskWaitingApproval {
				if err := rootTask.Transition(domain.TaskApproved); err != nil {
					return rootTask, err
				}
				e.publishTaskState(eventbus.TaskApproved, rootTask)
			}
			continue
		}

		// Drive the state machine for executable steps. The task advances
		// along the canonical lifecycle to the step's target state, so a
		// workflow may open at any stage (e.g. an incident workflow starting
		// at "plan") without listing every intermediate state.
		if next, ok := taskStateForAction[step.Action]; ok {
			if err := driveToState(rootTask, next); err != nil {
				return rootTask, err
			}
		}

		agentID, err := e.agentForStep(step)
		if err != nil {
			_ = rootTask.Fail(err.Error())
			e.persist(rootTask)
			e.publishTaskState(eventbus.TaskFailed, rootTask)
			return rootTask, err
		}
		rootTask.AgentID = agentID

		out, err := stepHandler(step.Action, rootTask)
		if err != nil {
			_ = rootTask.Fail(err.Error())
			rootTask.AddStep(Step{Action: step.Action, AgentID: agentID, Result: err.Error(), Status: "failed"})
			e.persist(rootTask)
			e.publishTaskState(eventbus.TaskFailed, rootTask)
			return rootTask, err
		}
		// Tool-output normalization. Large raw step outputs are
		// reduced to a compact summary (facts/errors/evidence/references) in
		// the step history — the active-context view — while the raw output
		// stays on the Task (rootTask.Output) and in the artifact chain, i.e.
		// available outside active context. This keeps the persisted step trail
		// lean without losing the original tool result.
		stepResult := out
		if len(out) > maxNormalizedStepOutput {
			norm := context.NormalizeToolResult(step.Action, out, 5)
			stepResult = norm.Summary
			if len(norm.Errors) > 0 {
				stepResult += "\n[errors]\n" + strings.Join(norm.Errors, "\n")
			}
			stepResult += fmt.Sprintf("\n[normalized %d → %d chars, raw in task output]", len(out), len(stepResult))
		}
		rootTask.AddStep(Step{Action: step.Action, AgentID: agentID, Result: stepResult, Status: "success"})
		rootTask.Output = out
	}

	// All steps complete: mark the task completed. The approval-gate resume
	// state is cleared so a completed task never resumes from an old gate.
	rootTask.ResumeStep = 0
	rootTask.ApprovalRefs = nil
	if err := rootTask.Complete(rootTask.Output); err != nil {
		return rootTask, err
	}
	e.persist(rootTask)
	e.publishTaskState(eventbus.TaskCompleted, rootTask)
	return rootTask, nil
}

// persist writes the task's current state to the registry's TaskStore (when
// set) so terminal transitions survive across sessions.
func (e *WorkflowEngine) persist(t *Task) {
	if st := e.registry.TaskStore(); st != nil {
		_, _ = st.Save(*t)
	}
}

// agentForStep resolves the agent for a workflow step, failing closed when the
// step's AgentType has no registered agent. "human" and empty AgentType mean
// no agent (approval gates).
func (e *WorkflowEngine) agentForStep(step WorkflowStep) (string, error) {
	if step.AgentType == "" || step.AgentType == "human" {
		return "", nil
	}
	agents := e.registry.ByType(step.AgentType)
	if len(agents) == 0 {
		return "", fmt.Errorf("agent: no %q agent registered", step.AgentType)
	}
	// Deterministic: pick the lexically smallest agent ID.
	return agents[0].ID, nil
}

// CompleteApproval marks a pending approval as granted, publishing
// task.approved on success. It errors if the approval is unknown or not pending.
func (e *WorkflowEngine) CompleteApproval(approvalID, approver string) error {
	if _, err := e.approvals.Approve(approvalID, approver); err != nil {
		return err
	}
	// Write the decision through the persistent store so an out-of-band
	// resolver (e.g. `kern approve`) and a fresh engine both observe it.
	if e.store != nil {
		if _, err := e.store.Decide(approvalID, approver, true, ""); err != nil {
			return err
		}
	}
	e.mu.Lock()
	ref, ok := e.approvalRef[approvalID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("agent: approval %q not tracked by engine", approvalID)
	}
	e.satisfied[ref.key] = true
	e.mu.Unlock()
	e.publish(eventbus.TaskApproved, taskIDForKey(ref), map[string]string{"approval": approvalID})
	return nil
}

// RejectApproval marks a pending approval as rejected. The task's gate stays
// unsatisfied; the caller should mark the task REJECTED. Errors if unknown.
func (e *WorkflowEngine) RejectApproval(approvalID, approver, reason string) error {
	if _, err := e.approvals.Reject(approvalID, approver, reason); err != nil {
		return err
	}
	if e.store != nil {
		if _, err := e.store.Decide(approvalID, approver, false, reason); err != nil {
			return err
		}
	}
	e.mu.Lock()
	ref, ok := e.approvalRef[approvalID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: approval %q not tracked by engine", approvalID)
	}
	e.publish(eventbus.TaskRejected, taskIDForKey(ref), map[string]string{"approval": approvalID, "reason": reason})
	return nil
}

// taskIDForKey extracts the task ID portion of a "<task>:<step>" approval key.
func taskIDForKey(a approval) string {
	if i := strings.IndexByte(a.key, ':'); i > 0 {
		return a.key[:i]
	}
	return a.key
}
