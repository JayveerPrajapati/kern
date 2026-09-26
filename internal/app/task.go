// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"log"
	"sync"
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
	// auditLog is the unified tamper-evident governance audit chain. Every
	// task state transition writes a task-lifecycle entry into it;
	// the write is best-effort and never blocks the transition. A nil log
	// (no wiring) is a no-op for back-compat. Defaults to the platform
	// firewall's audit log when the platform carries one.
	auditLog *governance.AuditLog
	// persistTasks gates whether read-only analysis commands write task
	// records to the persisted task store. It defaults to false: analysis
	// commands (Analyze/AnalyzeWithLens/WhatIf/Plan/Impact) are
	// workflow-state-free unless a caller explicitly opts in via
	// WithTaskPersistence(true). Workflow commands (loop/do/execute/workflow/
	// incident) create their tasks via Create and always persist — this flag
	// never touches them.
	persistTasks bool
	// ephemeral IDs the tasks created WITHOUT persistence (analysis commands),
	// so persist/fail skip the store for exactly those tasks while workflow
	// tasks persist unconditionally.
	ephemeral map[string]bool
	ephMu     sync.RWMutex
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
		// Read-only analysis commands do not persist task records unless a
		// caller explicitly opts in (F9): analysis is workflow-state-free,
		// audit transitions and artifacts are the log and still record.
		persistTasks: false,
		ephemeral:    map[string]bool{},
		// Default to the platform's unified audit chain so task
		// transitions land in the same tamper-evident log the firewall
		// writes. Nil-safe: a platform without a firewall (test literals)
		// yields a nil log = no-op.
		auditLog: platformAuditLog(p),
	}
}

// platformAuditLog returns the platform's unified audit log, or nil when the
// platform is nil or carries no firewall (nil = transition auditing no-op).
func platformAuditLog(p *Platform) *governance.AuditLog {
	if p == nil {
		return nil
	}
	if fw := p.Firewall(); fw != nil {
		return fw.AuditLog()
	}
	return nil
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

// WithAuditLog overrides the unified tamper-evident audit chain task
// transitions are recorded into. NewTaskService defaults it to the platform
// firewall's audit log; passing nil restores the no-op mode (transitions are
// not audited), which is the back-compat behavior for services wired without
// an audit log.
func (s *TaskService) WithAuditLog(a *governance.AuditLog) *TaskService {
	s.auditLog = a
	return s
}

// WithTaskPersistence opts a TaskService into (on=true) or out of (on=false)
// writing task records to the persisted task store for read-only analysis
// commands (Analyze/AnalyzeWithLens/WhatIf/Plan/Impact). It defaults to
// false — analysis commands do not persist workflow state unless explicitly
// opted in (surfaces that need an authoritative task record, e.g. a caller
// passing --task, call WithTaskPersistence(true)). Workflow commands
// (loop/do/execute/workflow/incident) always persist regardless of this
// setting: they create their tasks via Create, not the analysis path.
func (s *TaskService) WithTaskPersistence(on bool) *TaskService {
	s.persistTasks = on
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
	nextAction := "execute workflow — drive the returned tools yourself (kern_verify/kern_exec/kern_validate) or run the autonomous loop via CLI: kern do \"" + intent + "\""
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

// SetTaskScope attaches the unified task scope (paths + envs) to a task. It is
// the single boundary that task-scoped confinement applies: the same
// TaskScope gates path access at the Execute boundary (TaskScope.ValidatePatch)
// and env-gated actions through the governance firewall. Interfaces set it
// once when a task is scoped; unset tasks fall back to an allow-all scope
// (deny nothing).
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
// (the Execute patch boundary and the governance firewall for env-gated
// actions).
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

// persist writes the task's current state to the store.
func (s *TaskService) persist(t *agent.Task) {
	if t == nil || s.isEphemeral(t.ID) {
		// Read-only analysis task (F9): keep the state in the in-memory
		// registry only — no task record and no snapshot. Audit transitions
		// and artifacts were already recorded; they are the log, not
		// workflow state.
		return
	}
	if s.store != nil {
		if _, err := s.store.Save(*t); err != nil {
			// Task persistence is best-effort (callers keep working when the
			// store fails), but a silent drop hides lost state: surface it
			// loudly so task loss across sessions is observable in logs.
			log.Printf("kern app: task %s state could not be persisted: %v", t.ID, err)
		}
	}
	if s.snapshots != nil {
		_ = s.snapshots.Record(*t)
	}
}

// Snapshots returns the snapshot store, for querying task history.
func (s *TaskService) Snapshots() *agent.SnapshotStore { return s.snapshots }

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
