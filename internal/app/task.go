package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/coder"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/planner"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TaskService creates, progresses, and persists Tasks through the lifecycle.
type TaskService struct {
	platform   *Platform
	registry   *agent.Registry
	store      *agent.TaskStore
	snapshots  *agent.SnapshotStore
	arts       *ArtifactStore
	bus        *eventbus.Bus
	agentID    string              // identity of the calling interface (Invariant 6)
	prProvider prprovider.Provider // PR creation provider (default Noop)
	deployer   deployment.Deployer // deployer for the Deploy method (default Noop)
	scopes     map[string]domain.TaskScope
	scopesMu   sync.RWMutex
	// traceRec records tool-decision traces for workflow steps when
	// set (optional; nil disables). It makes the tool-selection trail
	// auditable: which tool ran for which step, why, and what it returned.
	traceRec *ToolDecisionTraceRecorder
	// sharedCorr is the single process-wide correlation service shared by the
	// correlate / investigate / deploy / observe lanes. It is built
	// lazily over the platform runtime source so every lane reasons over the
	// same source and lookback window.
	sharedCorr *runtime.SharedCorrelator
	// workflowRuns tracks in-flight agent-team runs : the task and its
	// WorkflowEngine are kept together so an approval-gated run can be resumed
	// with the SAME task + engine after the human resolves the gate. The engine
	// resumes at the gate step (progress) and reuses the task's state machine,
	// which a fresh task+engine could not. Entries are evicted when the run
	// reaches a terminal task state.
	workflowRuns map[string]*workflowRun
	wfMu         sync.Mutex
}

// workflowRun pairs a task with its driving workflow engine.
type workflowRun struct {
	task   *agent.Task
	engine *agent.WorkflowEngine
}

// NewTaskService creates a TaskService for the given Platform.
func NewTaskService(p *Platform, bus *eventbus.Bus) *TaskService {
	reg := agent.NewRegistry()
	store := agent.NewTaskStore(p.Root())
	reg.SetTaskStore(store)
	if bus != nil {
		reg.WithBus(bus)
	}
	return &TaskService{
		platform:     p,
		registry:     reg,
		store:        store,
		snapshots:    agent.NewSnapshotStore(p.Root()),
		arts:         NewArtifactStore(p.Root()),
		bus:          bus,
		agentID:      "kern", // default identity; override via WithAgentID
		prProvider:   prprovider.NoopProvider{},
		deployer:     deployment.NewDeployerFromEnv(),
		scopes:       map[string]domain.TaskScope{},
		workflowRuns: map[string]*workflowRun{},
	}
}

// WithDeployer sets the deployer used by the Deploy method. If not called, the
// service resolves one from the environment (KERN_DEPLOY_COMMAND) at
// construction time; unset → NoopDeployer (simulated success). This setter is
// primarily for tests.
func (s *TaskService) WithDeployer(d deployment.Deployer) *TaskService {
	if d != nil {
		s.deployer = d
	}
	return s
}

// WithAgentID sets the agent identity for this TaskService (Invariant 6).
// Interfaces should call this to distinguish themselves: MCP passes "mcp",
// CLI passes "cli", Web passes "web". The identity is stamped on every Task
// created by this service and recorded in audit entries so the audit trail
// carries WHO performed each action, not just THAT it happened.
func (s *TaskService) WithAgentID(id string) *TaskService {
	if id != "" {
		s.agentID = id
	}
	return s
}

// WithTraceRecorder attaches a tool-decision trace recorder. When
// set, every workflow step run through RunWorkflow records a ToolDecisionTrace
// (tool, why selected, expected output, actual output, latency) so the tool
// selection trail is auditable.
func (s *TaskService) WithTraceRecorder(r *ToolDecisionTraceRecorder) *TaskService {
	s.traceRec = r
	return s
}

// AgentID returns the agent identity for this service.
func (s *TaskService) AgentID() string { return s.agentID }

// WithPRProvider sets the PR provider for this service. If not called, the
// service uses NoopProvider (render body only, no network). Callers that want
// real PR creation should pass prprovider.NewGitHubProvider() (which returns
// nil if KERN_GITHUB_TOKEN is unset, and the service falls back to Noop).
func (s *TaskService) WithPRProvider(p prprovider.Provider) *TaskService {
	if p != nil {
		s.prProvider = p
	}
	return s
}

// AutoPRProvider returns a GitHubProvider if KERN_GITHUB_TOKEN is set,
// otherwise NoopProvider. This is a convenience for callers that want
// env-driven PR creation without explicit wiring.
func AutoPRProvider() prprovider.Provider {
	if g := prprovider.NewGitHubProvider(); g != nil {
		return g
	}
	return prprovider.NoopProvider{}
}

// Registry returns the task registry backing this service.
func (s *TaskService) Registry() *agent.Registry { return s.registry }

// Store returns the persisted task store backing this service.
func (s *TaskService) Store() *agent.TaskStore { return s.store }

// Artifacts returns the artifact store backing this service, for querying the
// linked artifact chain via Get/GetByTask/List.
func (s *TaskService) Artifacts() *ArtifactStore { return s.arts }

// Risk runs the context engine against a proposed change and returns a focused
// risk view (level, factors, mitigation) rather than the full packet. It is the
// app-layer (TaskService) equivalent of Platform.Risk — the shared method the
// CLI (`kern risk`) and REST (`POST /v1/risk`) both call. It makes Risk a
// first-class application service so interfaces never reach into the engine
// directly.
func (s *TaskService) Risk(change string) (domain.ContextPacket, string, error) {
	if s.platform == nil {
		return domain.ContextPacket{}, "", fmt.Errorf("task service: platform not configured")
	}
	return s.platform.Risk(change)
}

// Firewall returns the shared governance firewall backing this service. It
// makes Policy a first-class application service: interfaces gate risk,
// permissions, and approvals through the single shared firewall instead of
// constructing their own.
func (s *TaskService) Firewall() *governance.Firewall {
	if s.platform == nil {
		return nil
	}
	return s.platform.Firewall()
}

// AuditLog returns the shared tamper-evident audit log backing this service. It
// makes Audit a first-class application service so interfaces read the same
// authoritative governance trail the running firewall writes.
func (s *TaskService) AuditLog() *governance.AuditLog {
	if fw := s.Firewall(); fw != nil {
		return fw.AuditLog()
	}
	return nil
}

// AuditEntries reads every persisted audit entry from the shared store under
// <root>/.kern/audit/, the same trail the running firewall writes. The
// in-memory AuditLog().All() only returns entries recorded in THIS process, so
// a fresh CLI/MCP process would otherwise see nothing. Entries whose files are
// missing/corrupt are skipped rather than aborting the listing; order matches
// the store's List order (legacy per-key files by key, then chain entries in
// append order).
func (s *TaskService) AuditEntries() ([]governance.AuditEntry, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("task service: platform not configured")
	}
	store := storage.NewLog(filepath.Join(s.platform.Root(), ".kern", "audit"))
	ctx := context.Background()
	entries, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range entries {
		var entry governance.AuditEntry
		if err := storage.UnmarshalValue(e.Value, &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// AuditEntriesForTask returns only the persisted audit entries whose TaskID
// matches, preserving order. It mirrors the CLI's filterByTask so every
// interface (CLI, MCP) filters through the service.
func (s *TaskService) AuditEntriesForTask(taskID string) ([]governance.AuditEntry, error) {
	entries, err := s.AuditEntries()
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range entries {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	return out, nil
}

// PendingApprovals returns the approvals awaiting a human decision, read from
// the same persistent store the workflow/deploy gates write. It makes Approval
// a first-class application service so interfaces never construct the file
// store themselves.
func (s *TaskService) PendingApprovals() ([]domain.Approval, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("task service: platform not configured")
	}
	return governance.NewFileStore(s.platform.Root()).Pending()
}

// ResolveApproval records a human decision on a pending approval. approve=true
// approves, false rejects. The decision is persisted to the shared store so a
// fresh process (or a resumed engine) observes it.
func (s *TaskService) ResolveApproval(id, approver string, approve bool, reason string) (domain.Approval, error) {
	if s.platform == nil {
		return domain.Approval{}, fmt.Errorf("task service: platform not configured")
	}
	return governance.NewFileStore(s.platform.Root()).Decide(id, approver, approve, reason)
}

// Agents returns the standard specialist team role list. It makes Agent a
// first-class application service: interfaces ask the service for the available
// specialist roles instead of importing the agents engine directly.
func (s *TaskService) Agents() []agents.RoleInfo {
	return agents.AllRoles()
}

// MemoryRecall recalls the up-to-5 most relevant past lessons for a query from
// the engineering memory store. It makes Memory a first-class application
// service, delegating to the same memory the analysis/incident engines read.
func (s *TaskService) MemoryRecall(query string) []string {
	if s.platform == nil {
		return nil
	}
	entries := memory.Recall(s.platform.Root(), query, 5)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out
}

// MemoryStore returns the shared engineering memory store. It exposes the
// underlying store for callers that need its richer API while keeping the
// recall/semantic path on MemoryRecall.
func (s *TaskService) MemoryStore() *memory.MemoryStore {
	if s.platform == nil {
		return nil
	}
	return s.platform.Memory()
}

// Create makes a new Task for the given intent and submits it to the registry.
// The Task starts in CREATED state with the intent as both Input and Intent.
// Returns the created Task (a pointer into the registry, so state mutations
// are visible) or an error if submission fails.
func (s *TaskService) Create(intent string) (*agent.Task, error) {
	t := agent.NewTask("analyze", intent)
	// The persisted store owns task IDs: clear the process-local ID assigned
	// by NewTask so SubmitTask lets the store assign "t-<max+1>" under its
	// cross-process file lock. Two processes would otherwise both start at
	// t-1 and Save (replace by ID) would silently destroy one of the tasks.
	t.ID = ""
	t.Intent = intent
	t.CreatedBy = s.agentID
	t.Requester = s.agentID
	if s.platform != nil {
		t.Project = filepath.Base(s.platform.Root())
	}
	if err := s.registry.SubmitTask(t); err != nil {
		return nil, fmt.Errorf("task service: %w", err)
	}
	s.publish(eventbus.TaskCreated, t.ID, map[string]string{"intent": intent})
	return t, nil
}

// Get returns a Task by ID. It checks the in-memory registry first, then falls
// back to the persisted store (so tasks from prior sessions are retrievable).
// Returns nil, false when the task is unknown.
func (s *TaskService) Get(id string) (*agent.Task, bool) {
	if t, ok := s.registry.GetTask(id); ok {
		return t, true
	}
	// Fall back to the persisted store for tasks from prior sessions.
	t, err := s.store.Get(id)
	if err != nil {
		return nil, false
	}
	return &t, true
}

// List returns all tasks known to this service, sorted by ID.
func (s *TaskService) List() []*agent.Task {
	return s.registry.ListTasks()
}

// Run is the kern_run entry point ( ). It compiles the
// intent, selects the workflow, runs a policy precheck, selects capabilities
// and tools, creates a Task, and returns a RunResult with the task ID,
// workflow, capabilities, tools, agents, risk, approval state, and next
// action.
// An external agent can call Run(intent) and Kern builds a valid Task/workflow
// without requiring the external agent to manually orchestrate low-level Kern
// tools.
func (s *TaskService) Run(intent string) (*domain.RunResult, error) {
	compiled := CompileIntent(intent)
	workflow := SelectWorkflow(compiled.Type)
	caps := DefaultCapabilities(compiled.Type)
	tools := CapabilitiesToTools(caps)
	agentIDs := CapabilitiesToAgents(caps)

	// Policy precheck: verify the agent identity is known and the intent type
	// is allowed. The firewall gates execution later; here we just assess risk.
	risk := domain.Risk{Level: domain.RiskLow}
	for _, c := range caps {
		if c.Risk == "high" {
			risk.Level = domain.RiskHigh
			risk.ApprovalRequired = true
		} else if c.Risk == "medium" && risk.Level == domain.RiskLow {
			risk.Level = domain.RiskMedium
		}
	}
	risk.Factors = []string{string(compiled.Type)}

	// Create the Task.
	t, err := s.Create(intent)
	if err != nil {
		return nil, err
	}

	// Unified policy precheck: run identity/scope/permission/
	// environment/risk through one gate so the caller can see the decision
	// before execution. It is advisory here (execution is gated separately);
	// the precheck result is surfaced on the RunResult.
	precheck := s.PolicyPrecheck(context.Background(), domain.PrecheckRequest{
		AgentID:     s.agentID,
		TaskID:      t.ID,
		Resource:    compiled.Scope,
		Action:      actionForIntent(compiled.Type),
		Environment: compiled.Environment,
		Scope: domain.TaskScope{
			TaskID: t.ID,
			Paths:  []string{compiled.Scope},
			Envs:   []string{compiled.Environment, "development", "staging"},
		},
	})

	// Determine approval state and next action.
	approvalState := "none"
	nextAction := "execute workflow"
	if risk.ApprovalRequired {
		approvalState = "required"
		nextAction = "request approval"
	}
	// A denied precheck is authoritative for the plan: the run is blocked by
	// policy before any execution, so the plan must not claim "execute
	// workflow". Execution is gated separately by the firewall, but the
	// RunResult's next action must stay consistent with the precheck it just
	// ran — an operator following the plan to the letter would otherwise be told
	// to execute a change its own policy precheck already denied.
	if precheck.Denied && precheck.DenyReason != nil {
		approvalState = "denied"
		nextAction = "precheck denied at " + precheck.DenyReason.Stage + ": " + precheck.DenyReason.Reason
	}

	result := &domain.RunResult{
		TaskID:        t.ID,
		Workflow:      workflow,
		Intent:        compiled,
		Capabilities:  capabilityNames(caps),
		Tools:         tools,
		Agents:        agentIDs,
		ContextPlan:   contextPlanFor(compiled.Type),
		Risk:          risk,
		ApprovalState: approvalState,
		NextAction:    nextAction,
		Precheck:      &precheck,
	}
	s.persist(t)
	return result, nil
}

// RunLoop is the task-scoped closed-loop entry point. It creates an
// authoritative Task for the intent, runs the closed loop at the requested
// autonomy level, records the run as an artifact, and returns the Task plus the
// loop Result so the interface layer can render it. It replaces the previous
// inline loop.NewLoop(...).Run(...) orchestration in the MCP handler: the
// service owns the loop so every interface gets task tracking and an audit
// trail. RunLoop runs the loop's default no-op stages (read-only).
func (s *TaskService) RunLoop(intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.runLoop(intent, level, false)
}

// RunDo is the task-scoped autonomous closed-loop entry point (the "Implement
// X" path). It behaves exactly like RunLoop but additionally wires the
// autonomous coder and the LLM-driven planner into the loop's default stage
// handlers, so `kern do "add a cache layer"` drives the full
// understand→remember→plan→code→verify→protect→observe→learn loop without a
// caller-supplied StepFunc. The coder and planner use the provider-neutral LLM
// factory (KERN_LLM_PROVIDER, default local Ollama); their stage gates sit at
// >= L2 autonomy, so L0/L1 runs never invoke them.
func (s *TaskService) RunDo(intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.runLoop(intent, level, true)
}

// runLoop is the shared task-scoped closed-loop implementation behind RunLoop
// and RunDo. When autonomous is true, the loop's default code and plan stages
// are handled by the coder and planner agents (mirroring what the CLI's runDo
// previously wired inline) instead of no-op'ing.
func (s *TaskService) runLoop(intent string, level loop.Autonomy, autonomous bool) (*agent.Task, *loop.Result, error) {
	if s.platform == nil {
		return nil, nil, fmt.Errorf("task service: platform not configured")
	}
	t, err := s.Create(intent)
	if err != nil {
		return nil, nil, err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	root := s.platform.Root()
	cfg := loop.LoopConfig{
		Root:     root,
		Level:    level,
		Mem:      memory.NewMemoryStore(root),
		Recorder: flight.New(root),
	}
	if autonomous {
		cfg.Coder = coder.New(agent.OllamaProvider())
		cfg.Planner = planner.New(agent.OllamaProvider())
	}
	l, err := loop.NewLoop(cfg)
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, err
	}
	if s.bus != nil {
		l.WithBus(s.bus)
	}

	res, err := l.Run(intent, nil)
	if res == nil {
		res = &loop.Result{Intent: intent, Level: level}
	}
	t.Output = fmt.Sprintf("level: %s, stages: %d, deployed: %v", level, len(res.Stages), res.Deployed)
	t.AddStep(agent.Step{
		Action:     "loop",
		AgentID:    "loop-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("deployed: %v, healthy: %v", res.Deployed, res.ObservedHealthy),
		Status:     "success",
	})

	// Record the loop run as an artifact in the audit chain.
	s.recordArtifact(domain.ArtifactPlan, t.ID, "loop-engine",
		fmt.Sprintf("loop run: %s, %d stages, deployed=%v", res.Intent, len(res.Stages), res.Deployed),
		"", "loop:run")

	if err != nil {
		s.fail(t, err.Error())
		return t, res, err
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, res, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, res, nil
}

// SetTaskScope attaches the unified task scope (paths + envs) to a task. It is
// the single boundary that task-scoped confinement applies: the same
// TaskScope gates path access at the Execute boundary (TaskScope.ValidatePatch),
// env-gated actions through the governance firewall, and any explicit
// per-resource check through authorizeResource. Interfaces set it once when a
// task is scoped; unset tasks fall back to an allow-all scope (deny nothing).
func (s *TaskService) SetTaskScope(taskID string, scope domain.TaskScope) {
	s.scopesMu.Lock()
	defer s.scopesMu.Unlock()
	if s.scopes == nil {
		s.scopes = map[string]domain.TaskScope{}
	}
	s.scopes[taskID] = scope
}

// TaskScope returns the unified scope registered for a task, or an allow-all
// scope when none was set. It is the single authoritative scope the service
// carries for a task; confinement is enforced where task-scoped actions occur
// (the Execute patch boundary, the governance firewall for env-gated actions,
// and authorizeResource for explicit per-resource checks).
func (s *TaskService) TaskScope(taskID string) domain.TaskScope {
	s.scopesMu.RLock()
	defer s.scopesMu.RUnlock()
	if s.scopes == nil {
		return domain.TaskScope{TaskID: taskID}
	}
	if sc, ok := s.scopes[taskID]; ok {
		return sc
	}
	return domain.TaskScope{TaskID: taskID}
}

// authorizeResource is the unified task-scope confinement primitive: it takes
// the task's SAME TaskScope and applies it uniformly regardless of the
// resource kind — context, memory, artifact, or runtime. A value outside the
// task's path/environment scope is denied for context, memory, artifacts, AND
// runtime alike: there is exactly one boundary, not four.
//
// It is NOT currently invoked on the resource-access paths. Task-scoped path
// confinement at the app layer is enforced through the SAME TaskScope by
// TaskScope.ValidatePatch in Execute/ExecuteAndVerify (every file a patch
// touches is checked against CheckPath before it is applied), and the
// environment dimension is enforced by the governance firewall in
// Deploy/PolicyPrecheck, which remain the primary gate for governance-level
// actions. The memory and runtime lanes (MemoryRecall, Correlate,
// InvestigateIncident, RemediateIncident) do not take a caller-supplied
// resource value, so they are not task-scoped resource accesses. authorizeResource
// is reserved as the explicit per-resource checkpoint for callers that DO
// access a resource by path/value within a task context.
// resourceKind is informational ("context", "memory", "artifact", "runtime")
// for provenance and auditing; the denial decision is uniform because it is
// derived from the task scope alone.
func (s *TaskService) authorizeResource(ctx context.Context, taskID, resourceKind, action, value string) (bool, string) {
	scope := s.TaskScope(taskID)
	if !scope.CheckPath(value) {
		return false, "resource " + value + " is outside the task scope for " + resourceKind
	}
	// Environment is a uniform policy dimension too: an action that requires a
	// forbidden environment is denied across every resource kind.
	if env, ok := actionEnv(action); ok && !scope.CheckEnv(env) {
		return false, "environment " + env + " is outside the task scope for " + resourceKind
	}
	return true, ""
}

// actionEnv extracts an environment from an action when the action encodes one
// (e.g. "read:production"), so the unified boundary can enforce the env gate on
// any resource kind.
func actionEnv(action string) (string, bool) {
	if i := strings.IndexByte(action, ':'); i > 0 && i < len(action)-1 {
		return action[i+1:], true
	}
	return "", false
}

// Cancel transitions a task to CANCELLED with a reason. The task is persisted
// and a task.updated event is published. Returns an error if the task is
// already terminal or the transition is invalid.
func (s *TaskService) Cancel(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Cancel(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "cancel", "reason": reason})
	return nil
}

// Timeout transitions a task to FAILED, indicating it exceeded a deadline.
func (s *TaskService) Timeout(taskID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Timeout(); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskFailed, t.ID, map[string]string{"reason": "timeout"})
	return nil
}

// Retry reopens a FAILED task to ANALYZING. Idempotent: if the task is already
// non-terminal, it is a no-op. The task is persisted and a task.updated event
// is published.
func (s *TaskService) Retry(taskID string) (*agent.Task, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	if err := t.Retry(); err != nil {
		return nil, err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{
		"action":       "retry",
		"retry_count":  fmt.Sprintf("%d", t.RetryCount),
		"retry_reason": t.RetryReason,
	})
	return t, nil
}

// Resume unblocks a BLOCKED task, returning it to its PriorState. Idempotent:
// if the task is already non-terminal, it is a no-op.
func (s *TaskService) Resume(taskID string) (*agent.Task, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	if err := t.Resume(); err != nil {
		return nil, err
	}
	// Full reconstruction on resume. The resumed task rehydrates
	// its ContextPacket and Plan from the persisted artifacts, so a resumed
	// task is not a shell — it carries the same working context it had when it
	// was paused/blocked. Best-effort: if reconstruction fails, resume still
	// succeeds (the task is usable with what it had).
	s.reconstructContext(t)
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "resume"})
	return t, nil
}

// reconstructContext rehydrates a task's ContextPacket and Plan from its
// persisted artifacts and rich context snapshot ( full
// reconstruction). It is best-effort and never fails the caller: it only
// restores fields that can be derived from the artifact chain or the most
// recent persisted snapshot. Existing fields (e.g. a ContextPacket already
// attached by a fresh analyze) are preserved; snapshot fields are layered on
// top when present.
func (s *TaskService) reconstructContext(t *agent.Task) {
	if t == nil {
		return
	}
	// Build a minimal packet from the artifact chain if the task has none yet.
	if t.ContextPacket == nil {
		pkt := &domain.ContextPacket{GeneratedAt: time.Now()}
		arts, err := s.arts.GetByTask(t.ID)
		if err == nil {
			for _, a := range arts {
				if a.Kind == domain.ArtifactContextPacket && a.Scope != "" {
					pkt.Task = a.Scope
					break
				}
			}
		}
		t.ContextPacket = pkt
	}
	// Layer the rich context snapshot (Goal, Decisions, Constraints, Files,
	// Tests, Risks) on top so a resumed task carries its prior working context.
	// Best-effort: only the most recent snapshot is considered.
	if s.snapshots == nil {
		return
	}
	snaps, err := s.snapshots.History(t.ID)
	if err != nil || len(snaps) == 0 {
		return
	}
	snap := snaps[len(snaps)-1]
	pkt := t.ContextPacket
	if pkt.Task == "" && snap.Goal != "" {
		pkt.Task = snap.Goal
	}
	addFacts := func(stmts []string) {
		for _, stmt := range stmts {
			if stmt == "" {
				continue
			}
			pkt.Facts = append(pkt.Facts, domain.Claim{
				Type:      domain.ClaimFact,
				Statement: stmt,
				Source:    "resume:snapshot",
			})
		}
	}
	addFacts(snap.Decisions)
	addFacts(snap.Constraints)
	addFacts(snap.Files)
	addFacts(snap.Tests)
	addFacts(snap.Risks)
}

// Pause blocks a task with a reason, recording its PriorState so Resume can
// return to it. Idempotent: if the task is already BLOCKED, it is a no-op. The
// task is persisted and a task.blocked event is published with the pause reason.
func (s *TaskService) Pause(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Pause(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskBlocked, t.ID, map[string]string{"action": "pause", "reason": reason})
	return nil
}

// Rollback transitions a PR_CREATED / DEPLOYING / OBSERVING task to
// ROLLED_BACK with a reason.
func (s *TaskService) Rollback(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Rollback(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "rollback", "reason": reason})
	return nil
}

// HumanTakeover blocks a task and binds it to a human agent, recording the
// prior state so it can be resumed after intervention.
func (s *TaskService) HumanTakeover(taskID, agentID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.HumanTakeover(agentID); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskBlocked, t.ID, map[string]string{"action": "human_takeover", "agent": agentID})
	return nil
}

// ReturnToAgent hands a human-takeover (BLOCKED) task back to an agent. It
// resumes the task to its prior state and reassigns the AgentID. Mirrors
// HumanTakeover's structure: get, mutate, persist, publish.
func (s *TaskService) ReturnToAgent(taskID, agentID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.ReturnToAgent(agentID); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "return_to_agent", "agent": agentID})
	return nil
}

// getTaskForMutation retrieves a task by ID from the registry or the persisted
// store, returning an error if not found.
func (s *TaskService) getTaskForMutation(taskID string) (*agent.Task, error) {
	t, ok := s.registry.GetTask(taskID)
	if ok {
		return t, nil
	}
	stored, err := s.store.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", taskID, err)
	}
	return &stored, nil
}

// RunWorkflowDefault is the exit-gate entry point: Kern selects and
// coordinates the agent team WITHOUT the external caller manually sequencing
// it. The caller passes only the intent — everything else is Kern's:
// 1. Task creation (Task →).
// 2. Agent selection: the task is classified by kind and the kind-specific
// workflow (only the specialists that apply) is registered — the same
// selection RunWorkflow performs.
// 3. Team wiring: the standard specialist team (planner, architect, coder,
// reviewer, security, tester, sre) is registered on the engine's registry
// so every workflow role resolves without external setup.
// 4. Coordination: the WorkflowEngine drives the steps (session → context →
// tool call → result → artifact → Task state) in order, parking at the
// human approval gate before the first execution step.
// 5. Execution: Kern's own default step handler performs each step — the
// analyze and plan steps run the real deterministic engines (platform
// analysis + plan assembly), and the remaining role stages produce
// deterministic outcomes from the task's real plan/risk/test data.
// The human approval gate is preserved (Invariant #2): the task parks in
// WAITING_FOR_APPROVAL and the error wraps agent.ErrApprovalRequired. The
// caller extracts the approval ID via agent.ApprovalID(err), resolves it via
// CompleteApproval (or out-of-band `kern approve`), and calls
// RunWorkflowResume — the engine resumes at the gate and drives the remaining
// steps to completion. The run state (resume step + approval bindings) is
// persisted on the task and the approval decision through the project's
// approval store, so resume also works across processes.
func (s *TaskService) RunWorkflowDefault(intent string) (*agent.Task, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, err
	}
	eng := s.engineForTask(t)

	// Keep the task + engine together so an approval-gated run can resume with
	// the same pair (the engine's gate/progress state lives on the instance).
	s.wfMu.Lock()
	s.workflowRuns[t.ID] = &workflowRun{task: t, engine: eng}
	s.wfMu.Unlock()

	return s.runStoredWorkflow(t.ID)
}

// fail marks a Task FAILED, persists it, and publishes a task.failed event.
func (s *TaskService) fail(t *agent.Task, errMsg string) {
	_ = t.Fail(errMsg)
	s.persist(t)
	s.publish(eventbus.TaskFailed, t.ID, map[string]string{"error": errMsg})
}

// persist writes the task's current state to the store.
func (s *TaskService) persist(t *agent.Task) {
	if s.store != nil {
		_, _ = s.store.Save(*t)
	}
	if s.snapshots != nil {
		_ = s.snapshots.Record(*t)
	}
}

// Snapshots returns the snapshot store, for querying task history.
func (s *TaskService) Snapshots() *agent.SnapshotStore { return s.snapshots }

// recordArtifact creates a domain.Artifact with the given kind and links it
// into the task's artifact chain via parentID. It persists the artifact to the
// ArtifactStore and appends the artifact ID to the Task's Artifacts slice.
// parentID may be empty (root artifact). provenance records how the artifact
// was produced (e.g. "context:analyze", "whatif:simulate").
func (s *TaskService) recordArtifact(kind domain.ArtifactKind, taskID, createdBy, scope, parentID, provenance string) {
	if s.arts == nil {
		return
	}
	art := domain.NewArtifact(kind, taskID, scope)
	art.CreatedBy = createdBy
	art.Status = "final"
	art.Scope = scope
	art.Provenance = provenance
	art.ParentArtifactID = parentID
	saved, err := s.arts.Save(art)
	if err != nil {
		return
	}
	// Append the artifact ID to the task's Artifacts slice so the chain is
	// reachable from the Task.
	if t, ok := s.registry.GetTask(taskID); ok {
		t.Artifacts = append(t.Artifacts, saved.ID)
	}
}

// lastArtifactID returns the ID of the most recent artifact of the given kind
// for a task, or "" when none exists. It is used to link a new artifact to its
// predecessor in the chain (e.g. ImpactReport → parent ContextPacket).
func (s *TaskService) lastArtifactID(taskID string, kind domain.ArtifactKind) string {
	if s.arts == nil {
		return ""
	}
	arts, err := s.arts.GetByTask(taskID)
	if err != nil {
		return ""
	}
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Kind == kind {
			return arts[i].ID
		}
	}
	return ""
}

// publish emits an event on the optional bus. Nil bus is a no-op.
func (s *TaskService) publish(kind eventbus.Kind, subject string, payload map[string]string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(eventbus.Event{
		// Give every event a deterministic ID derived from its
		// content (kind + subject + canonical payload). The bus dedups on
		// non-empty IDs, so re-publishing an identical event (a retried
		// producer, or a duplicated transition) is a no-op instead of
		// duplicating side effects, while distinct state changes (different
		// payload) still flow. Go's json.Marshal sorts map keys, so the
		// payload serialization is canonical.
		ID:      stableEventID(kind, subject, payload),
		Kind:    kind,
		Source:  "app",
		Subject: subject,
		Payload: payload,
	})
}

// stableEventID derives a deterministic, content-addressed event ID. Identical
// (kind, subject, payload) triples hash to the same ID so the bus's
// idempotency layer drops duplicate deliveries; different payloads (e.g. a
// later state in a transition chain) yield different IDs and flow normally.
func stableEventID(kind eventbus.Kind, subject string, payload map[string]string) string {
	pb, _ := json.Marshal(payload)
	sum := sha256.Sum256([]byte(string(kind) + "|" + subject + "|" + string(pb)))
	return fmt.Sprintf("e-%x", sum[:12])
}
