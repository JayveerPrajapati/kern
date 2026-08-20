package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// ErrApprovalRequired is returned by Run when a step requires human approval
// that has not been granted. The caller must call CompleteApproval and re-run.
var ErrApprovalRequired = errors.New("agent: human approval required")

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

	e.mu.Lock()
	start := e.progress[rootTask.ID]
	e.mu.Unlock()
	for i := start; i < len(wf.Steps); i++ {
		step := wf.Steps[i]

		// Abort if the task reached a terminal state mid-flight.
		if rootTask.IsTerminal() {
			return rootTask, fmt.Errorf("agent: task %s reached terminal state %s before step %d", rootTask.ID, rootTask.State, i)
		}

		// Human approval gate.
		if step.RequiresApproval {
			key := approvalKey(rootTask.ID, i)
			e.mu.Lock()
			sat := e.satisfied[key]
			e.mu.Unlock()
			if !sat {
				if err := rootTask.Transition(domain.TaskWaitingApproval); err != nil {
					return rootTask, err
				}
				req := e.approvals.Request(rootTask.ID, rootTask.CreatedBy, step.Action)
				e.mu.Lock()
				e.approvalRef[req.ID] = approval{key: key}
				// Resume at this gate step on re-run.
				e.progress[rootTask.ID] = i
				e.mu.Unlock()
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

		// Drive the state machine for executable steps.
		if next, ok := taskStateForAction[step.Action]; ok {
			if rootTask.State != next {
				if err := rootTask.Transition(next); err != nil {
					return rootTask, err
				}
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
		rootTask.AddStep(Step{Action: step.Action, AgentID: agentID, Result: out, Status: "success"})
		rootTask.Output = out
	}

	// All steps complete: mark the task completed.
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
