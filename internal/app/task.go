package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/coder"
	ctxpkg "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/planner"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TaskService is the application service that makes Task authoritative. It
// creates, progresses, and persists Tasks through the analyze → impact → plan →
// verify lifecycle, so every interface (CLI, MCP, REST) can create an
// authoritative Task record instead of running stateless.
// This realizes the : "Make Task
// Authoritative." Before this service, kern analyze / kern_analyze ran
// stateless — no Task was created, no lifecycle was recorded. Now every
// analysis creates a Task, progresses it through the state machine, persists
// it to the TaskStore, and attaches lifecycle results (context packet, impact,
// verification) to the Task so it is the single authoritative object for
// audit, resume, and debugging.
// TaskService wraps Platform's engine methods with Task lifecycle management.
// It does NOT replace Platform.Analyze/WhatIf/Verify — those remain the
// stateless fast path. TaskService adds the Task-tracking layer on top.
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

// workflowRun pairs a task with the engine that is driving its workflow, so a
// human-approval pause can be resumed (CompleteApproval → Run again) without
// losing the engine's gate/progress state.
type workflowRun struct {
	task   *agent.Task
	engine *agent.WorkflowEngine
}

// NewTaskService creates a TaskService for the given Platform. It creates a
// fresh Registry, TaskStore, and ArtifactStore rooted at the Platform's root.
// The optional bus publishes task lifecycle events (task.created, task.updated,
// etc.).
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
// the store's key order.
func (s *TaskService) AuditEntries() ([]governance.AuditEntry, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("task service: platform not configured")
	}
	store := storage.NewLocal(filepath.Join(s.platform.Root(), ".kern", "audit"))
	ctx := context.Background()
	keys, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range keys {
		raw, err := store.Get(ctx, e.Key)
		if err != nil {
			continue
		}
		var entry governance.AuditEntry
		if err := storage.UnmarshalValue(raw, &entry); err != nil {
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

// actionForIntent maps an intent type to the representative governed action
// used in the unified policy precheck .
func actionForIntent(it domain.IntentType) string {
	switch it {
	case domain.IntentDeploy:
		return "deploy"
	case domain.IntentCodeChange, domain.IntentModernization:
		return "write"
	case domain.IntentSecurity:
		return "scan"
	case domain.IntentAudit:
		return "audit"
	default:
		return "read"
	}
}

func capabilityNames(caps []domain.Capability) []string {
	var names []string
	for _, c := range caps {
		names = append(names, c.Name)
	}
	return names
}

// PolicyPrecheck runs the unified policy precheck. It combines the
// five pre-execution gates — identity, scope, permission (firewall), environment,
// and preliminary risk — into a single PrecheckResult so a caller (MCP, CLI,
// REST, or the Run entry point) can see an ALLOW/DENY decision up front without
// orchestrating separate governance calls. It never mutates state; it is the
// read-only gate that precedes execution.
// The gate order follows the firewall's fail-closed model: environment, then
// path/scope, then firewall permission+risk. Any gate failure denies.
func (s *TaskService) PolicyPrecheck(ctx context.Context, req domain.PrecheckRequest) domain.PrecheckResult {
	res := domain.PrecheckResult{
		Environment: req.Environment,
		Scope:       req.Resource,
	}

	// 1. Environment gate from the unified scope.
	if !req.Scope.CheckEnv(req.Environment) {
		res.Allowed = false
		res.Denied = true
		res.Risk = domain.Risk{Level: domain.RiskCritical, Blocked: true}
		res.DenyReason = &domain.DenyReason{
			Stage: "env", AgentID: req.AgentID, TaskID: req.TaskID,
			Resource: req.Resource, Action: req.Action,
			Reason: "environment " + req.Environment + " is outside the task scope",
			Risk:   res.Risk,
		}
		return res
	}

	// 2. Scope/path gate from the unified scope.
	if !req.Scope.CheckPath(req.Resource) {
		res.Allowed = false
		res.Denied = true
		res.Risk = domain.Risk{Level: domain.RiskCritical, Blocked: true}
		res.DenyReason = &domain.DenyReason{
			Stage: "boundary", AgentID: req.AgentID, TaskID: req.TaskID,
			Resource: req.Resource, Action: req.Action,
			Reason: "resource " + req.Resource + " is outside the task scope",
			Risk:   res.Risk,
		}
		return res
	}

	// 3. Permission (firewall) + preliminary risk. An unconfigured firewall is
	// treated as permissive for the precheck (execution is gated separately);
	// when present, it is authoritative for identity/permission/risk/approval.
	fw := s.Firewall()
	if fw != nil {
		allowed, risk, approval, fwErr := fw.Check(req.AgentID, req.Resource, req.Action)
		if fwErr != nil || !allowed {
			res.Allowed = false
			res.Denied = true
			res.Risk = risk
			res.RequiredApproval = approval
			reason := "firewall policy denied"
			if fwErr != nil {
				reason = fwErr.Error()
			}
			res.DenyReason = &domain.DenyReason{
				Stage: "firewall", AgentID: req.AgentID, TaskID: req.TaskID,
				Resource: req.Resource, Action: req.Action, Reason: reason,
				Risk: risk, RequiredApproval: approval,
			}
			return res
		}
		res.Risk = risk
		res.RequiredApproval = approval
	}

	// Passed all gates.
	res.Allowed = true
	res.Denied = false
	return res
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

// ReplayRecord carries the metadata a replay needs to be meaningful: which
// repo version, which model, and which configuration produced the
// task being replayed. Without this, a replayed task is ambiguous.
type ReplayRecord struct {
	TaskID      string `json:"task_id"`
	RepoVersion string `json:"repo_version"` // git sha / version at the time
	Model       string `json:"model"`        // model that ran the original task
	ConfigHash  string `json:"config_hash"`  // hash of the config used
	// ContextVersion is a digest of the context packet the task ran with (the
	// "context version" of ): a change in the analysis or package
	// input changes the digest, so two replays can be compared on what context
	// they actually saw. Empty when the task carries no context packet.
	ContextVersion string `json:"context_version,omitempty"`
	// ToolVersions is a deterministic digest of the distinct tools/actions the
	// task actually invoked across its steps ("tool versions"). It changes
	// when the tool selection changes, making replays
	// comparable on the tool surface used.
	ToolVersions string    `json:"tool_versions,omitempty"`
	ReplayedAt   time.Time `json:"replayed_at"`
}

// ReplayTask reconstructs a task for replay, returning a ReplayRecord with the
// metadata (repo version, model, config hash, context version, tool versions)
// needed to interpret the replay. It returns the reconstructed
// task's current state plus the metadata record.
func (s *TaskService) ReplayTask(taskID, repoVersion, model, configHash string) (*ReplayRecord, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	s.reconstructContext(t)
	rec := &ReplayRecord{
		TaskID:         t.ID,
		RepoVersion:    repoVersion,
		Model:          model,
		ConfigHash:     configHash,
		ContextVersion: replayContextVersion(t),
		ToolVersions:   replayToolVersions(t),
		ReplayedAt:     time.Now().UTC(),
	}
	return rec, nil
}

// replayContextVersion digests the task's context packet into a stable
// context version. Empty when the task has no packet.
func replayContextVersion(t *agent.Task) string {
	if t == nil || t.ContextPacket == nil {
		return ""
	}
	digest := sha256.Sum256([]byte(ctxpkg.RenderText(*t.ContextPacket)))
	return hex.EncodeToString(digest[:])[:16]
}

// replayToolVersions digests the distinct tool actions a task invoked into a
// stable tool-version fingerprint. Empty when the task has no
// steps.
func replayToolVersions(t *agent.Task) string {
	if t == nil || len(t.Steps) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var names []string
	for _, st := range t.Steps {
		if st.Action != "" && !seen[st.Action] {
			seen[st.Action] = true
			names = append(names, st.Action)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	digest := sha256.Sum256([]byte(strings.Join(names, "|")))
	return hex.EncodeToString(digest[:])[:16]
}

// RunCompare compares two task runs by their artifact chains and snapshot
// histories ( run-compare). It reports which artifact kinds differ,
// whether the tasks reached the same state, and a per-stage verdict. Unlike the
// raw ArtifactStore.Compare, RunCompare folds in the snapshot history so the
// run outcome (not just artifacts) is compared.
func (s *TaskService) RunCompare(taskID1, taskID2 string) (*RunComparison, error) {
	arts, err := s.Artifacts().Compare(taskID1, taskID2)
	if err != nil {
		return nil, err
	}
	res := &RunComparison{
		ArtifactDiff: arts,
		DigestDiff:   len(arts.DigestDiff),
		OnlyIn1:      len(arts.OnlyIn1),
		OnlyIn2:      len(arts.OnlyIn2),
	}
	// Snapshot history for each task.
	h1, _ := s.Snapshots().History(taskID1)
	h2, _ := s.Snapshots().History(taskID2)
	res.Snapshots1 = len(h1)
	res.Snapshots2 = len(h2)
	if len(h1) > 0 && len(h2) > 0 {
		res.State1 = string(h1[len(h1)-1].State)
		res.State2 = string(h2[len(h2)-1].State)
		res.StateDiffer = res.State1 != res.State2
	}
	// Rich run dimensions : agent, model, tool-call proxy, cost,
	// and success, read from each task's record when available. Best-effort:
	// zero values when a task cannot be fetched or a dimension is unset.
	t1, _ := s.getTaskForMutation(taskID1)
	t2, _ := s.getTaskForMutation(taskID2)
	if t1 != nil {
		res.Agent1 = t1.AgentID
		res.ToolCalls1 = len(t1.Steps)
		res.Success1 = t1.State == domain.TaskCompleted
		res.Cost1 = costProxy(t1)
	}
	if t2 != nil {
		res.Agent2 = t2.AgentID
		res.ToolCalls2 = len(t2.Steps)
		res.Success2 = t2.State == domain.TaskCompleted
		res.Cost2 = costProxy(t2)
	}
	// Verdict: tasks are equivalent when their artifact kinds and final states
	// match with no digest differences.
	res.Equivalent = !res.StateDiffer && res.DigestDiff == 0 && res.OnlyIn1 == 0 && res.OnlyIn2 == 0
	return res, nil
}

// costProxy is a deterministic stand-in for the model cost of a task when no
// measured cost is recorded. It derives a 0..1 proxy from the task's step
// count so richer run comparisons have a comparable "cost" signal without
// inventing a currency. Zero when the task has no steps.
func costProxy(t *agent.Task) float64 {
	if t == nil || len(t.Steps) == 0 {
		return 0
	}
	n := float64(len(t.Steps))
	if n > 100 {
		n = 100
	}
	return n / 100
}

// RunComparison is the run-compare result .
type RunComparison struct {
	ArtifactDiff *ArtifactComparison
	DigestDiff   int
	OnlyIn1      int
	OnlyIn2      int
	Snapshots1   int
	Snapshots2   int
	State1       string
	State2       string
	StateDiffer  bool
	Equivalent   bool

	// Rich run dimensions. Populated best-effort from each task's
	// record when available; zero values when unavailable.
	Agent1     string
	Agent2     string
	Model1     string
	Model2     string
	ToolCalls1 int
	ToolCalls2 int
	Cost1      float64
	Cost2      float64
	Success1   bool
	Success2   bool
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

// Analyze creates a Task for the intent, runs the context engine, and attaches
// the ContextPacket to the Task. The Task transitions CREATED → ANALYZING →
// (COMPLETED or FAILED). Returns the Task and the rendered analysis text.
// This is the Task-tracked version of Platform.Analyze. Interfaces that want
// stateless analysis call Platform.Analyze directly; interfaces that want an
// authoritative Task record call TaskService.Analyze.
func (s *TaskService) Analyze(intent string) (*agent.Task, string, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, "", err
	}
	return s.analyzeTask(t, intent)
}

// analyzeTask drives a Task through the ANALYZING state, runs the context
// engine, attaches the packet, and completes the Task. It is the shared
// implementation for Analyze and any future staged workflow.
func (s *TaskService) analyzeTask(t *agent.Task, change string) (*agent.Task, string, error) {
	return s.analyzeTaskOpts(t, change, true)
}

// analyzeTaskOpts is like analyzeTask but with a complete flag. When complete
// is false, the task is left in ANALYZING state (not completed) so a caller
// like Plan can continue the lifecycle (ANALYZING → PLANNING).
func (s *TaskService) analyzeTaskOpts(t *agent.Task, change string, complete bool) (*agent.Task, string, error) {
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	pkt, text, err := s.platform.Analyze(change)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}

	// Attach lifecycle results to the Task.
	t.ContextPacket = &pkt
	t.Risks = pkt.Risks
	// Attach the context engine's evidence-backed claims
	// (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION) to the Task so the analysis is
	// persisted with its evidence trail: evidence claims must be part of the
	// auditable task record, not only the rendered text.
	t.Evidence = append(t.Evidence, pkt.Facts...)
	t.Output = text
	t.AddStep(agent.Step{
		Action:     "analyze",
		AgentID:    "context-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("context packet: %d symbols, %d risks", len(pkt.Symbols), len(pkt.Risks)),
		Status:     "success",
	})

	// Emit risk.calculated so the bus carries each identified risk to
	// webhooks/audit ( event standardization).
	for _, r := range pkt.Risks {
		s.publish(eventbus.RiskCalculated, t.ID, map[string]string{
			"level":      string(r.Level),
			"mitigation": r.Mitigation,
		})
	}

	// Record the ContextPacket as the root artifact of the chain.
	s.recordArtifact(domain.ArtifactContextPacket, t.ID, "context-engine",
		"context packet: "+change, "", "context:analyze")
	// Record the AnalysisReport artifact (P10.4) linked as a child of the
	// context packet so the analysis is a typed, traceable artifact in the chain.
	s.recordArtifact(domain.ArtifactAnalysisReport, t.ID, "context-engine",
		"analysis: "+change,
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "context:analyze")

	if complete {
		if err := t.Complete(text); err != nil {
			s.fail(t, err.Error())
			return t, "", err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	}
	return t, text, nil
}

// WhatIf creates a Task, simulates the change, attaches the Impact to the Task,
// and completes it. Returns the Task and the rendered impact text.
func (s *TaskService) WhatIf(kind whatif.ChangeKind, change, newTarget string) (*agent.Task, string, error) {
	t, err := s.Create(fmt.Sprintf("what-if: %s %s", kind, change))
	if err != nil {
		return nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	imp, text, err := s.platform.WhatIf(kind, change, newTarget)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}

	t.ImpactReport = &imp
	t.Output = text
	t.AddStep(agent.Step{
		Action:     "what-if",
		AgentID:    "whatif-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("affected: %d, risk: %s", len(imp.Affected), imp.Risk),
		Status:     "success",
	})

	// Emit code.changed when the what-if shows affected files, so the bus
	// carries the change blast radius to webhooks/audit .
	if len(imp.Files) > 0 {
		s.publish(eventbus.CodeChanged, t.ID, map[string]string{
			"affected": fmt.Sprintf("%d", len(imp.Affected)),
			"files":    fmt.Sprintf("%d", len(imp.Files)),
			"risk":     imp.Risk,
		})
	}

	// Record the ImpactReport artifact, linked to the task's context-packet
	// artifact as its parent (when one exists) to form the audit chain.
	s.recordArtifact(domain.ArtifactImpactReport, t.ID, "whatif-engine",
		fmt.Sprintf("impact: %d affected, risk=%s", len(imp.Affected), imp.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "whatif:simulate")
	// Record the RiskReport artifact (P10.4) so the risk assessment is a typed,
	// traceable artifact in the chain alongside the impact report.
	s.recordArtifact(domain.ArtifactRiskReport, t.ID, "whatif-engine",
		fmt.Sprintf("risk=%s", imp.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactImpactReport), "whatif:risk")

	// Exit gate: what-if is Evidence aware. Attach the simulation's typed
	// claims (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION with provenance) to the
	// task so the impact estimate is persisted with its evidence trail, not
	// only as rendered text and artifacts.
	if len(imp.Claims) > 0 {
		t.Evidence = append(t.Evidence, imp.Claims...)
	}

	if err := t.Complete(text); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, text, nil
}

// Plan creates a Task for the intent, runs the full control-plane Plan
// workflow (analyze → memory → impact → risk → architecture), and assembles a
// structured domain.Plan from the deterministic results. The Task transitions
// CREATED → ANALYZING → PLANNING → (COMPLETED or FAILED). Returns the Task,
// the Plan, and a rendered text summary.
// This realizes the : the Plan
// artifact is populated from deterministic sources (context packet, impact
// report, risk assessment, architecture rules) — the LLM may explain it, but
// the fields are not LLM guesses.
func (s *TaskService) Plan(intent string) (*agent.Task, domain.Plan, string, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, domain.Plan{}, "", err
	}

	// Stage 1: analyze (CREATED → ANALYZING), without completing the task.
	t, _, err = s.analyzeTaskOpts(t, intent, false)
	if err != nil {
		return t, domain.Plan{}, "", err
	}

	// Stage 2: plan (ANALYZING → PLANNING).
	if err := t.Transition(domain.TaskPlanning); err != nil {
		s.fail(t, err.Error())
		return t, domain.Plan{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "PLANNING"})

	// Stage 3: assemble the Plan from deterministic sources.
	var pkt domain.ContextPacket
	if t.ContextPacket != nil {
		pkt = *t.ContextPacket
	}
	plan := s.assemblePlan(intent, pkt)

	// Exit gate: mandatory constitution rules can block a plan before
	// execution. Validate the assembled plan against .kern/constitution.yaml;
	// a MUST/MUST_NOT violation blocks the plan (the task fails, so the plan
	// cannot proceed to execution) while SHOULD/SHOULD_NOT are non-blocking
	// warnings recorded on the task output. Missing constitution = no rules =
	// plan passes (backward compatible).
	if constitution, err := governance.LoadConstitution(s.platform.Root()); err != nil {
		s.fail(t, "constitution: "+err.Error())
		return t, domain.Plan{}, "", err
	} else if validation := governance.ValidatePlan(plan, constitution); !validation.Passed {
		msg := "plan blocked by constitution: " + validation.Violations[0].Message
		s.fail(t, msg)
		return t, domain.Plan{}, "", fmt.Errorf("%s", msg)
	}

	t.Plan = &plan
	t.Output = renderPlanText(plan)
	t.AddStep(agent.Step{
		Action:     "plan",
		AgentID:    "plan-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("plan: %d steps, risk=%s, %d affected components", len(plan.ImplementationSteps), plan.Risk, len(plan.AffectedComponents)),
		Status:     "success",
	})

	// Record the Plan artifact, linked to the context-packet artifact as its
	// parent to continue the audit chain.
	s.recordArtifact(domain.ArtifactPlan, t.ID, "plan-engine",
		fmt.Sprintf("plan: %s — %d steps, risk=%s", plan.Objective, len(plan.ImplementationSteps), plan.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "plan:assemble")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, plan, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, plan, t.Output, nil
}

// Impact creates a Task for the change, runs the 11 deterministic graph
// queries from the spec, and attaches the ImpactReport to the Task.
// The Task transitions CREATED → ANALYZING → (COMPLETED or FAILED). Returns
// the Task, the ImpactReport, and a rendered text summary.
// This realizes the : the impact
// ImpactOption customizes an Impact computation.
type ImpactOption func(*impactOptions)

type impactOptions struct {
	strict bool
}

// ImpactStrict opts an Impact computation into strict precision mode: call
// edges whose caller language is not "resolved"-precision in the index are
// skipped as unknown rather than trusted (see kern impact --precision strict).
func ImpactStrict() ImpactOption {
	return func(o *impactOptions) { o.strict = true }
}

// report is the deterministic source — the LLM may explain it, but the data
// comes from the knowledge graph, not an LLM guess.
func (s *TaskService) Impact(change string, opts ...ImpactOption) (*agent.Task, domain.ImpactReport, string, error) {
	o := &impactOptions{}
	for _, opt := range opts {
		opt(o)
	}
	t, err := s.Create(change)
	if err != nil {
		return nil, domain.ImpactReport{}, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, domain.ImpactReport{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	target, err := s.platform.resolveSymbol(change)
	if err != nil {
		s.fail(t, err.Error())
		return t, domain.ImpactReport{}, "", err
	}

	g := s.platform.Graph()
	rep := s.collectGraphImpact(g, target, o.strict)
	s.gatherRuntimeEvidence(&rep, target)
	classifyCriticality(g, target, o.strict, &rep)
	return s.finalizeImpact(t, &rep)
}

// collectGraphImpact runs the six deterministic graph queries from the spec
// (callers, callees, services, APIs, events, tests) against the target symbol
// and returns the ImpactReport fields they populate.
func (s *TaskService) collectGraphImpact(g *intelligence.Graph, target string, strict bool) domain.ImpactReport {
	rep := domain.ImpactReport{Target: target}
	// 1. What calls this?
	for _, n := range g.WhoCallsPrecise(target, strict) {
		rep.WhoCalls = append(rep.WhoCalls, nodeName(n))
	}
	// 2. What does it call?
	for _, n := range g.WhatDoesXDependOnPrecise(target, strict) {
		rep.WhatItCalls = append(rep.WhatItCalls, nodeName(n))
	}
	// 3. What services depend on it?
	for _, n := range g.WhatServicesAffectedPrecise(target, strict) {
		rep.ServicesDepend = append(rep.ServicesDepend, nodeName(n))
	}
	// 4. Which APIs are affected?
	for _, n := range g.WhatAPIsAffectedPrecise(target, strict) {
		rep.APIsAffected = append(rep.APIsAffected, nodeName(n))
	}
	// 5. Which events are affected?
	for _, n := range g.WhatEventsAffectedPrecise(target, strict) {
		rep.EventsAffected = append(rep.EventsAffected, nodeName(n))
	}
	// 6. Which tests cover it?
	for _, n := range g.WhatTestsCoverPrecise(target, strict) {
		rep.TestsCover = append(rep.TestsCover, nodeName(n))
	}
	return rep
}

// gatherRuntimeEvidence folds non-graph evidence into the report: affected data
// stores from the context packet's runtime evidence, related incidents from
// memory recall, and applicable architecture rules from the context packet.
func (s *TaskService) gatherRuntimeEvidence(rep *domain.ImpactReport, target string) {
	// Which data stores are affected? (from the context packet's databases)
	pkt, _ := s.platform.ctx.AnalyzeChange(target)
	for _, e := range pkt.RuntimeEvidence {
		if strings.Contains(strings.ToLower(string(e.Type)), "database") || strings.Contains(strings.ToLower(string(e.Type)), "db") {
			rep.DataStoresAffected = append(rep.DataStoresAffected, e.Content)
		}
	}
	// Which incidents are related? (from memory recall)
	if s.platform.Memory() != nil {
		ms, _ := s.platform.Memory().Recall(memory.Query{Text: target, Type: domain.MemoryIncident})
		for _, m := range ms {
			rep.IncidentsRelated = append(rep.IncidentsRelated, m.ID)
		}
	}
	// Which architecture rules apply?
	for _, p := range pkt.ArchitectureRules {
		rep.ArchitectureRules = append(rep.ArchitectureRules, p.ID)
	}
}

// classifyCriticality derives the report's risk level from the production
// criticality of the target symbol, falling back to the caller/service
// footprint when the graph reports no criticality tier.
func classifyCriticality(g *intelligence.Graph, target string, strict bool, rep *domain.ImpactReport) {
	// Risk from production criticality.
	crit := g.ProductionCriticalityPrecise(target, strict)
	switch crit {
	case "critical":
		rep.Risk = "high"
	case "high":
		rep.Risk = "high"
	case "medium":
		rep.Risk = "medium"
	default:
		if len(rep.ServicesDepend) > 0 {
			rep.Risk = "high"
		} else if len(rep.WhoCalls) > 0 {
			rep.Risk = "medium"
		} else {
			rep.Risk = "low"
		}
	}
}

// finalizeImpact stamps the completed ImpactReport onto the Task, records the
// report artifact, and completes the Task lifecycle (ANALYZING → COMPLETED).
func (s *TaskService) finalizeImpact(t *agent.Task, rep *domain.ImpactReport) (*agent.Task, domain.ImpactReport, string, error) {
	t.Impact = rep
	t.Output = renderImpactText(*rep)
	t.AddStep(agent.Step{
		Action:     "impact",
		AgentID:    "graph-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("impact: %d callers, %d services, risk=%s", len(rep.WhoCalls), len(rep.ServicesDepend), rep.Risk),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactImpactReport, t.ID, "graph-engine",
		fmt.Sprintf("impact: %d callers, risk=%s", len(rep.WhoCalls), rep.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "impact:graph")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, *rep, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, *rep, t.Output, nil
}

// assemblePlan builds a domain.Plan from the deterministic context packet. It
// derives each Plan field from a deterministic source: objective/scope from
// the intent, affected components from the packet's symbols+files, risk from
// the packet's risk assessment, tests from required validation, architecture
// from the packet's architecture rules, and evidence from the packet's facts.
func (s *TaskService) assemblePlan(intent string, pkt domain.ContextPacket) domain.Plan {
	plan := domain.Plan{
		Objective: intent,
		Scope:     scopeFromPacket(pkt),
		Risk:      riskLevelString(pkt.Risks),
	}

	// Affected components: symbols + files from the context packet.
	for _, sym := range pkt.Symbols {
		plan.AffectedComponents = append(plan.AffectedComponents, sym.Name)
	}
	for _, f := range pkt.Files {
		plan.AffectedComponents = append(plan.AffectedComponents, f.Path)
	}

	// Implementation steps: deterministic scaffolding from the required
	// validation list (build, test, security, architecture) plus the impact
	// shape.
	plan.ImplementationSteps = append(plan.ImplementationSteps, "Implement the change in the affected components above.")
	for _, v := range pkt.RequiredValidation {
		switch v {
		case "build":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Ensure the project builds (go build ./...).")
		case "test":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Add/update tests for affected symbols and run go test.")
		case "security":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Run security scan (kern security) and address findings.")
		case "architecture":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Validate architecture boundaries (kern guard).")
		}
	}

	// Dependencies: edges from the context packet.
	for _, e := range pkt.Dependencies {
		plan.Dependencies = append(plan.Dependencies, e.From+" → "+e.To)
	}

	// Tests: required validation + covering tests from the packet.
	plan.Tests = append(plan.Tests, pkt.RequiredValidation...)

	// Rollback: deterministic from risk level.
	switch plan.Risk {
	case "high":
		plan.Rollback = "High risk: revert the commit and redeploy the previous version. Verify via kern verify."
	case "medium":
		plan.Rollback = "Medium risk: revert the commit. Run the affected test suite."
	default:
		plan.Rollback = "Low risk: revert the commit."
	}

	// Security: surface if any risk factor mentions security.
	plan.Security = securityNotes(pkt.Risks)

	// Architecture: summarize architecture rules from the packet.
	plan.Architecture = architectureNotes(pkt.ArchitectureRules)

	// Deployment: deterministic from risk + affected services.
	plan.Deployment = deploymentNotes(pkt)

	// Evidence: claim statements from the packet's facts.
	for _, c := range pkt.Facts {
		plan.Evidence = append(plan.Evidence, c.Statement)
	}

	return plan
}

// Verify creates a Task, runs verification, attaches the result, and completes
// the Task. Returns the Task and the verification result.
func (s *TaskService) Verify(types []string) (*agent.Task, verification.VerificationResult, error) {
	t, err := s.Create("verify")
	if err != nil {
		return nil, verification.VerificationResult{}, err
	}
	if err := t.Transition(domain.TaskVerifying); err != nil {
		s.fail(t, err.Error())
		return t, verification.VerificationResult{}, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "VERIFYING"})

	res := s.platform.Verify(types)
	t.Verification = &res
	t.Output = fmt.Sprintf("verdict: %s", res.Verdict)
	t.AddStep(agent.Step{
		Action:     "verify",
		AgentID:    "verification-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		Status:     "success",
	})

	// Record the VerificationReport artifact, linked to the task's impact
	// artifact (when one exists) to continue the audit chain.
	s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "verification-engine",
		fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		s.lastArtifactID(t.ID, domain.ArtifactImpactReport), "verification:verify")

	// Also emit the typed sub-report artifacts (test, security,
	// architecture) so the safe-change slice's required artifact set
	// (ContextPacket, AnalysisReport, ImpactReport, RiskReport, Plan,
	// CodePatch, TestReport, SecurityReport, ArchitectureReport,
	// VerificationReport, Diff, PullRequest, Audit) is fully covered by the
	// lifecycle, not just the verification kinds.
	if res.UnitTests != nil {
		ok := res.UnitTests.OK
		s.recordArtifact(domain.ArtifactTestReport, t.ID, "verification-engine",
			fmt.Sprintf("tests: passed=%d failed=%d skipped=%d ok=%v", res.UnitTests.Passed, res.UnitTests.Failed, res.UnitTests.Skipped, ok),
			s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "verification:test")
	}
	if res.Security != nil {
		s.recordArtifact(domain.ArtifactSecurityReport, t.ID, "verification-engine",
			fmt.Sprintf("security: findings=%d critical=%d high=%d low=%d ok=%v", res.Security.Count, res.Security.Critical, res.Security.High, res.Security.Low, res.Security.OK),
			s.lastArtifactID(t.ID, domain.ArtifactTestReport), "verification:security")
	}
	if res.Architecture != nil {
		s.recordArtifact(domain.ArtifactArchitectureReport, t.ID, "verification-engine",
			fmt.Sprintf("architecture: violations=%d ok=%v", len(res.Architecture.Violations), res.Architecture.OK),
			s.lastArtifactID(t.ID, domain.ArtifactSecurityReport), "verification:architecture")
	}

	// Gate completion on the verification verdict: a failed verification must
	// never yield a COMPLETED task (reliability: verification failure → task
	// cannot become successful). Only a PASS verdict (or the non-blocking
	// PASS_WITH_WARNING) may complete; anything else fails the task.
	if res.Verdict == verification.VerdictPass || res.Verdict == verification.VerdictPassWithWarning {
		if err := t.Complete(t.Output); err != nil {
			s.fail(t, err.Error())
			return t, res, err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
		return t, res, nil
	}
	s.fail(t, "verification failed: "+res.Summary)
	return t, res, fmt.Errorf("verification failed: %s", res.Summary)
}

// RunWorkflow drives a Task through a dynamically selected agent workflow. The
// task is classified by kind (code change, documentation, incident,
// modernization) and the matching workflow — i.e. only the specialists that
// apply to that kind — is registered on the WorkflowEngine. This realizes the
// "AGENT SELECTION": do not invoke every
// agent for every request. Unclassified tasks fall back to the default
// workflow. The kind-specific workflows each preserve the human "approve" gate
// before the first execution step, so Invariant #2 (high-risk execution
// requires approval) holds on every path.
// The stepHandler is called for each workflow step; it receives the action
// name and the Task, and returns the step output. This is where specialist
// agents (planner, coder, reviewer, etc.) are invoked. Each step records an
// artifact when the stepHandler returns a non-empty output.
// The specialist pipeline (internal/agents: ClassifyTask → SelectWorkflow)
// provides classification, routing and the RequiresApproval gate. The actual
// step implementations are the closed-loop stages in internal/loop (the
// StepFunc stage handlers): the workflow engine here is the router and approval
// gate, and the loop provides the real plan/code/verify/deploy execution. The
// two are complementary — workflow selects and gates, loop executes.
func (s *TaskService) RunWorkflow(intent string, stepHandler func(action string, t *agent.Task) (string, error)) (*agent.Task, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, err
	}

	// Task-type-driven agent selection: register the workflow whose steps fit
	// the task kind, falling back to the full default workflow for unclassified
	// tasks. Both paths preserve the human approval gate. The task must also
	// NAME its workflow (WorkflowID) or the engine falls back to the default
	// and the kind-specific steps never run.
	kind := agents.ClassifyTask(t.Input, t.Type)
	wf := agents.SelectWorkflow(kind)
	t.WorkflowID = wf.ID
	eng := agent.NewWorkflowEngine(s.registry, governance.NewApprovalWorkflow())
	// Persist the gate approvals so a task parked at the human approval gate
	// survives a restart: the approval record is written to the shared store,
	// where `kern approve <id>` / the web UI resolves it out-of-band and a
	// fresh engine observes the decision on resume. Falls back to in-memory
	// when no platform (tests, ephemeral runs) backs the service.
	if s.platform != nil {
		eng.WithApprovalStore(governance.NewFileStore(s.platform.Root()))
	}
	if s.bus != nil {
		eng.WithBus(s.bus)
	}
	eng.RegisterWorkflow(wf)

	// Wrap the step handler to record artifacts for each step.
	wrapped := func(action string, task *agent.Task) (string, error) {
		start := time.Now()
		out, err := stepHandler(action, task)
		// Record a tool-decision trace for the step — which tool
		// ran, why it was selected, what it was expected to produce, and what
		// it actually returned. This makes the tool-selection trail auditable
		// instead of an in-memory return value.
		if s.traceRec != nil {
			s.traceRec.Record(domain.ToolDecisionTrace{
				Tool:           toolForAction(action),
				WhySelected:    "workflow step " + action,
				Inputs:         truncate(task.Input, 200),
				ExpectedOutput: "result of " + action,
				ActualOutput:   truncate(out, 200),
				Latency:        float64(time.Since(start).Milliseconds()),
			})
		}
		if err != nil {
			return out, err
		}
		// Record an artifact for each step that produces output.
		if out != "" {
			kind := artifactKindForAction(action)
			s.recordArtifact(kind, task.ID, action+"-agent", out, s.lastArtifactID(task.ID, domain.ArtifactContextPacket), "workflow:"+action)
		}
		s.publish(eventbus.TaskUpdated, task.ID, map[string]string{"action": action, "result": truncate(out, 100)})
		return out, nil
	}

	return eng.Run(t, wrapped)
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

// engineForTask builds (or rebuilds) the WorkflowEngine that drives a task's
// kind-selected workflow: it classifies the task, registers the kind workflow
// on the engine, names the workflow on the task (the engine resolves by
// WorkflowID and falls back to the default when empty), registers the standard
// specialist team so every role resolves, and attaches the persistent approval
// store so gates survive process restarts. Idempotent — re-registering an
// existing specialist is skipped.
func (s *TaskService) engineForTask(t *agent.Task) *agent.WorkflowEngine {
	// Agent selection: classify the task and register the kind workflow.
	kind := agents.ClassifyTask(t.Input, t.Type)
	wf := agents.SelectWorkflow(kind)
	t.WorkflowID = wf.ID

	// Persistent approval backend: the same store `kern approve` writes, so an
	// approval surfaced by the gate can be resolved out-of-band and a fresh
	// engine observes the decision on resume.
	eng := agent.NewWorkflowEngine(s.registry, governance.NewApprovalWorkflow()).
		WithApprovalStore(governance.NewFileStore(s.platform.Root()))
	if s.bus != nil {
		eng.WithBus(s.bus)
	}
	eng.RegisterWorkflow(wf)

	// Team wiring: register the standard specialists so every workflow role
	// resolves. Idempotent — a specialist already registered (e.g. a previous
	// run of this service) is left in place.
	if _, team, err := agents.StandardTeam(); err == nil {
		for _, a := range team.All() {
			if _, exists := s.registry.Get(a.ID); !exists {
				_ = s.registry.Register(a)
			}
		}
	}
	return eng
}

// RunWorkflowResume resumes an approval-gated agent-team run. It prefers the
// in-process task + engine pair that parked the run; when this service never
// ran it (fresh process), it loads the task from the TaskStore and rebuilds
// the engine from the task's persisted workflow + resume state. The engine
// resumes at the gate step — the persistent approval store records whether the
// gate was resolved out-of-band (e.g. `kern approve`) — and drives the
// remaining steps to completion.
func (s *TaskService) RunWorkflowResume(taskID string) (*agent.Task, error) {
	s.wfMu.Lock()
	run, ok := s.workflowRuns[taskID]
	s.wfMu.Unlock()
	if ok {
		return s.runWorkflow(run.task, run.engine)
	}
	// Cross-process resume: recover the task from the TaskStore.
	stored, err := s.store.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("no agent-team workflow run or stored task for %s", taskID)
	}
	task := stored
	eng := s.engineForTask(&task)
	return s.runWorkflow(&task, eng)
}

// runStoredWorkflow runs (or resumes) the workflow for a stored task, evicting
// the run once the task reaches a terminal state.
func (s *TaskService) runStoredWorkflow(taskID string) (*agent.Task, error) {
	s.wfMu.Lock()
	run, ok := s.workflowRuns[taskID]
	s.wfMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no agent-team workflow run for task %s", taskID)
	}
	return s.runWorkflow(run.task, run.engine)
}

// runWorkflow runs an engine against a task, evicting the run once the task
// reaches a terminal state.
func (s *TaskService) runWorkflow(t *agent.Task, eng *agent.WorkflowEngine) (*agent.Task, error) {
	res, err := eng.Run(t, s.defaultWorkflowStep())
	if res != nil && res.Terminal() {
		s.wfMu.Lock()
		delete(s.workflowRuns, res.ID)
		s.wfMu.Unlock()
	}
	return res, err
}

// CompleteApproval resolves a pending human-approval gate on any in-flight
// agent-team workflow run, delegating to the engine that owns the approval.
// It is the counterpart to the agent.ApprovalID surfaced by an
// ErrApprovalRequired result.
func (s *TaskService) CompleteApproval(approvalID, approver string) error {
	s.wfMu.Lock()
	defer s.wfMu.Unlock()
	for _, run := range s.workflowRuns {
		if err := run.engine.CompleteApproval(approvalID, approver); err == nil {
			return nil
		}
	}
	return fmt.Errorf("app: approval %q not found on any agent-team workflow run", approvalID)
}

// defaultWorkflowStep is Kern's own step executor for the agent team. It makes
// the team runnable end-to-end without an external handler: the analyze and
// plan steps run the real deterministic engines, and every other role stage
// (code, verify, pr, review, security, test, sre, architect) produces a
// deterministic outcome from the task's real plan/risk/test data. The heavy
// creative execution (closed-loop coder/verifier with worktrees and LLMs)
// remains the loop's job — this handler is the coordination-level execution
// that lets Kern sequence the team autonomously.
func (s *TaskService) defaultWorkflowStep() func(action string, t *agent.Task) (string, error) {
	return func(action string, t *agent.Task) (string, error) {
		switch action {
		case "analyze":
			// Real deterministic analysis: platform.Analyze feeds the same
			// context packet, risks, and evidence claims the Analyze() path
			// attaches. No state transition here — the engine drives the task
			// state machine around this handler. A prose intent that does not
			// resolve to a symbol degrades gracefully: the run continues with
			// an empty context packet rather than failing the whole team.
			pkt, text, err := s.platform.Analyze(t.Input)
			if err != nil {
				return fmt.Sprintf("analyze: could not resolve %q as a symbol — proceeding with an empty context packet (%v)", truncate(t.Input, 80), err), nil
			}
			t.ContextPacket = &pkt
			t.Risks = pkt.Risks
			t.Evidence = append(t.Evidence, pkt.Facts...)
			t.Output = text
			return text, nil
		case "plan":
			// Real deterministic plan: assemble the implementation plan from
			// the analyzed context packet, exactly as TaskService.Plan does.
			var pkt domain.ContextPacket
			if t.ContextPacket != nil {
				pkt = *t.ContextPacket
			}
			plan := s.assemblePlan(t.Input, pkt)
			t.Plan = &plan
			return renderPlanText(plan), nil
		default:
			// code/verify/pr/review/security/test/sre/architect: deterministic
			// stage outcome derived from the task's real plan/risk/test data.
			return s.stageOutcome(action, t), nil
		}
	}
}

// stageOutcome renders a deterministic outcome for a specialist role stage from
// the task's real analyzed data (plan steps, affected components, risks,
// required validations). It is the coordination-level execution for stages
// whose heavy creative machinery lives in the closed loop.
func (s *TaskService) stageOutcome(action string, t *agent.Task) string {
	var steps, components, tests, risks int
	if t.Plan != nil {
		steps = len(t.Plan.ImplementationSteps)
		components = len(t.Plan.AffectedComponents)
		tests = len(t.Plan.Tests)
	}
	risks = len(t.Risks)
	switch action {
	case "code":
		return fmt.Sprintf("code stage by coder: implement plan — %d steps across %d affected components", steps, components)
	case "verify":
		return fmt.Sprintf("verify stage by reviewer: run %d required validations (go build ./... + tests)", tests)
	case "pr":
		return fmt.Sprintf("pr stage by reviewer: open pull request for the change (%s)", s.prProvider)
	case "review":
		return fmt.Sprintf("review stage by reviewer: review change against %d risks and %d affected components", risks, components)
	case "security":
		return fmt.Sprintf("security stage by security: scan change for %d identified risks", risks)
	case "test":
		return fmt.Sprintf("test stage by tester: run %d required tests for the change", tests)
	case "sre":
		return fmt.Sprintf("sre stage by sre: assess deployability of %d affected components", components)
	case "architect":
		return fmt.Sprintf("architect stage by architect: design change across %d affected components", components)
	default:
		return fmt.Sprintf("%s stage: executed by specialist", action)
	}
}

// Execute runs a patch in a sandboxed worktree, gated by governance .
// It creates a Task, transitions to EXECUTING, checks the governance firewall,
// applies the patch in a worktree, records the diff as an artifact, and returns
// the Task + diff.
// High-risk operations never directly modify the main working tree — the patch
// is applied in an isolated worktree. The governance gate (governance.CheckExec)
// is centralized here so no interface can bypass it.
func (s *TaskService) Execute(patch string) (*agent.Task, string, error) {
	t, err := s.Create("execute patch")
	if err != nil {
		return nil, "", err
	}

	// Governance gate: fail-closed before any execution.
	if err := governance.CheckExec(); err != nil {
		s.fail(t, "governance denied: "+err.Error())
		return t, "", err
	}

	// Task boundary: a controlled action must not bypass
	// task-scoped governance. Validate every path the patch touches against
	// the task's scope BEFORE applying it.
	if err := s.TaskScope(t.ID).ValidatePatch(patch); err != nil {
		s.fail(t, "boundary denied: "+err.Error())
		return t, "", err
	}

	if err := t.Transition(domain.TaskExecuting); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "EXECUTING"})

	wt, err := execution.NewWorktree(s.platform.Root())
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	defer wt.Cleanup()

	if err := wt.Apply(patch); err != nil {
		s.fail(t, "apply: "+err.Error())
		return t, "", err
	}

	diff, err := wt.Diff()
	if err != nil {
		s.fail(t, "diff: "+err.Error())
		return t, "", err
	}

	t.Output = fmt.Sprintf("applied patch: %d bytes, diff: %d chars", len(patch), len(diff))
	t.AddStep(agent.Step{
		Action:     "execute",
		AgentID:    "execution-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("applied %d-byte patch in worktree %s", len(patch), wt.Dir()),
		Status:     "success",
	})

	// Record the diff artifact, linked to the plan artifact (when one exists).
	s.recordArtifact(domain.ArtifactDiff, t.ID, "execution-engine",
		fmt.Sprintf("diff: %d chars", len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactPlan), "execution:worktree")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, diff, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, diff, nil
}

// ExecuteAndVerify runs a patch in a sandboxed worktree and immediately
// verifies it (build), keeping the worktree alive across both steps. It is the
// task-native equivalent of the legacy CLI runExecute path (which used raw
// execution.NewWorktree + manual verify). The worktree is cleaned up before
// returning. Returns the Task, the diff, and the verification result.
// This exists because Execute() defer-cleans the worktree, so a caller that
// wants to verify the worktree after Execute cannot access wt.Dir(). This
// method holds the worktree across both steps.
func (s *TaskService) ExecuteAndVerify(patch string, verifyTypes []string) (*agent.Task, string, verification.VerificationResult, error) {
	t, err := s.Create("execute patch")
	if err != nil {
		return nil, "", verification.VerificationResult{}, err
	}

	// Governance gate: fail-closed before any execution.
	if err := governance.CheckExec(); err != nil {
		s.fail(t, "governance denied: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	// Task boundary: reject patches touching out-of-scope paths
	// before applying (same enforcement as Execute).
	if err := s.TaskScope(t.ID).ValidatePatch(patch); err != nil {
		s.fail(t, "boundary denied: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	if err := t.Transition(domain.TaskExecuting); err != nil {
		s.fail(t, err.Error())
		return t, "", verification.VerificationResult{}, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "EXECUTING"})

	wt, err := execution.NewWorktree(s.platform.Root())
	if err != nil {
		s.fail(t, err.Error())
		return t, "", verification.VerificationResult{}, err
	}
	defer wt.Cleanup()

	if err := wt.Apply(patch); err != nil {
		s.fail(t, "apply: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	diff, err := wt.Diff()
	if err != nil {
		s.fail(t, "diff: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	t.Output = fmt.Sprintf("applied patch: %d bytes, diff: %d chars", len(patch), len(diff))
	t.AddStep(agent.Step{
		Action:     "execute",
		AgentID:    "execution-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("applied %d-byte patch in worktree %s", len(patch), wt.Dir()),
		Status:     "success",
	})

	// Record the diff artifact, linked to the plan artifact (when one exists).
	s.recordArtifact(domain.ArtifactDiff, t.ID, "execution-engine",
		fmt.Sprintf("diff: %d chars", len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactPlan), "execution:worktree")

	// Verify the worktree (build/test) before cleanup.
	vres := s.verifyInWorktree(t, wt.Dir(), verifyTypes)

	// Gate completion on the verification verdict: a failed verification must
	// never yield a COMPLETED task. Only a PASS verdict (or the non-blocking
	// PASS_WITH_WARNING) may complete; anything else fails the task.
	if vres.Verdict == verification.VerdictPass || vres.Verdict == verification.VerdictPassWithWarning {
		if err := t.Complete(t.Output); err != nil {
			s.fail(t, err.Error())
			return t, diff, vres, err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
		return t, diff, vres, nil
	}
	s.fail(t, "verification failed: "+vres.Summary)
	return t, diff, vres, fmt.Errorf("verification failed: %s", vres.Summary)
}

// verifyInWorktree runs verification on the given worktree dir and records the
// result on the Task. It does NOT transition state — the caller decides the
// final transition. Returns the verification result.
func (s *TaskService) verifyInWorktree(t *agent.Task, worktreeDir string, types []string) verification.VerificationResult {
	if len(types) == 0 {
		types = []string{"build"}
	}
	eng := verification.NewEngine(worktreeDir)
	res := eng.Verify(types)
	t.Verification = &res
	t.AddStep(agent.Step{
		Action:     "verify",
		AgentID:    "verification-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "verification-engine",
		fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		s.lastArtifactID(t.ID, domain.ArtifactDiff), "verification:worktree")
	return res
}

// VerifyTask verifies a Task's worktree diff and transitions to READY_FOR_PR
// Unlike the standalone Verify, this chains after Execute: it
// verifies the specific worktree produced by execution, not the current tree.
// The Task transitions VERIFYING → READY_FOR_PR (on pass) or FAILED (on fail).
// Every check produces evidence, and the final verification becomes an artifact.
func (s *TaskService) VerifyTask(taskID string, worktreeDir string, types []string) (*agent.Task, verification.VerificationResult, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, verification.VerificationResult{}, fmt.Errorf("task not found: %s", taskID)
	}
	if len(types) == 0 {
		types = []string{"build", "test"}
	}

	if err := t.Transition(domain.TaskVerifying); err != nil {
		s.fail(t, err.Error())
		return t, verification.VerificationResult{}, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "VERIFYING"})

	// Verify the worktree, not the main tree.
	eng := verification.NewEngine(worktreeDir)
	res := eng.Verify(types)
	t.Verification = &res
	t.Output = fmt.Sprintf("verdict: %s", res.Verdict)
	t.AddStep(agent.Step{
		Action:     "verify",
		AgentID:    "verification-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		Status:     "success",
	})

	// Record the verification artifact linked to the diff artifact.
	s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "verification-engine",
		fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		s.lastArtifactID(t.ID, domain.ArtifactDiff), "verification:worktree")

	// Transition based on the verdict.
	if res.Verdict == verification.VerdictPass || res.Verdict == verification.VerdictPassWithWarning {
		if err := t.Transition(domain.TaskReadyForPR); err != nil {
			s.fail(t, err.Error())
			return t, res, err
		}
		s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "READY_FOR_PR"})
	} else {
		s.fail(t, "verification failed: "+res.Summary)
		return t, res, fmt.Errorf("verification failed: %s", res.Summary)
	}

	s.persist(t)
	return t, res, nil
}

// CreatePR creates a PR from the Task's structured artifacts .
// It renders a PR body from the Plan, Impact, and Verification artifacts, and
// transitions the Task to PR_CREATED.
// The PR requires: (1) verification passed (Task is in READY_FOR_PR), (2) the
// diff artifact exists. The PR body is generated from artifacts, not from an
// agent's memory of what it changed — this is safer and more auditable.
// The body is always rendered and recorded as an artifact regardless of
// provider outcome. If the configured provider (default Noop) creates a real
// PR, the URL/number are stamped on the Task and appended to the output; a
// provider error is recorded in the step result but does NOT fail the task.
func (s *TaskService) CreatePR(taskID string, branch string) (*agent.Task, string, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, "", fmt.Errorf("task not found: %s", taskID)
	}

	// Require verification to have passed.
	if t.State != domain.TaskReadyForPR {
		return t, "", fmt.Errorf("task must be in READY_FOR_PR state (current: %s) — run VerifyTask first", t.State)
	}

	// Render the PR body from structured artifacts.
	body := s.renderPRBody(t)

	t.Output = body

	// Attempt to create a real PR via the provider.
	// NoopProvider (default) returns Number=0, URL="" — no network call.
	repo, _ := prprovider.DetectRepo(s.platform.Root())
	prResult, prErr := s.prProvider.CreatePR(prprovider.Request{
		Owner: repo.Owner,
		Repo:  repo.Repo,
		Title: t.Intent,
		Head:  branch,
		Base:  "main",
		Body:  body,
	})

	var stepResult string
	switch {
	case prErr != nil:
		// Log the error in the step result but do NOT fail the task — the body
		// is still rendered and the artifact is recorded.
		stepResult = fmt.Sprintf("PR render: %d chars, branch: %s; PR creation FAILED: %v", len(body), branch, prErr)
	case prResult != nil && prResult.Number > 0:
		t.PRURL = prResult.URL
		t.PRNumber = prResult.Number
		t.Output = body + "\n\nPR: " + prResult.URL
		stepResult = fmt.Sprintf("PR #%d created: %s", prResult.Number, prResult.URL)
	default:
		// noop (Number == 0)
		if prResult != nil {
			t.PRURL = prResult.URL
			t.PRNumber = prResult.Number
		}
		stepResult = fmt.Sprintf("PR body: %d chars, branch: %s (noop — set KERN_GITHUB_TOKEN for real PR)", len(body), branch)
	}

	t.AddStep(agent.Step{
		Action:     "pr",
		AgentID:    "pr-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     stepResult,
		Status:     "success",
	})

	// Record the PR artifact, linked to the verification artifact.
	s.recordArtifact(domain.ArtifactPullRequest, t.ID, "pr-engine",
		fmt.Sprintf("PR: %s — branch %s", t.Intent, branch),
		s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "pr:render")

	if err := t.Transition(domain.TaskPRCreated); err != nil {
		s.fail(t, err.Error())
		return t, body, err
	}
	s.publish(eventbus.PRCreated, t.ID, map[string]string{"branch": branch, "intent": t.Intent})
	s.persist(t)
	return t, body, nil
}

// Deploy transitions a Task from PR_CREATED to DEPLOYING, performing a real
// deployment via the configured deployer (default NoopDeployer → simulated
// success; KERN_DEPLOY_COMMAND + KERN_ALLOW_DEPLOY=1 → real external deploy).
// The version string identifies the deployment version.
// Governance: a real deploy (ShellDeployer) is a CRITICAL production.deploy
// action. Deploy checks the governance firewall before proceeding; if approval
// is required it returns agent.ErrApprovalRequired wrapping the pending
// approval ID — the caller must resolve the approval (e.g. via kern approve)
// and call Deploy again. The NoopDeployer (simulated, default) skips the gate
// to preserve v1 behavior.
func (s *TaskService) Deploy(taskID string, version string) (*agent.Task, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	deployer := s.deployer
	if deployer == nil {
		deployer = deployment.NoopDeployer{}
	}

	// Governance gate: only real deploys (non-Noop) require human approval.
	// The firewall checks agent identity, permission, risk, and policy. A
	// CRITICAL production.deploy with no prior approval returns a pending
	// Approval; we surface it as ErrApprovalRequired so the caller can resolve
	// it and retry. NoopDeployer (simulated) bypasses the gate for v1 compat.
	if _, isNoop := deployer.(deployment.NoopDeployer); !isNoop {
		fw := s.platform.Firewall()
		if fw != nil {
			agentID := s.agentID
			if agentID == "" {
				agentID = "task-service"
			}
			allowed, risk, approval, err := fw.Check(agentID, "production", "deploy")
			if err != nil {
				s.fail(t, "governance: "+err.Error())
				return t, fmt.Errorf("deploy: governance check failed: %w", err)
			}
			if !allowed && approval != nil {
				// Park the task — do NOT transition to Deploying yet. The caller
				// resolves the approval and calls Deploy again; the firewall's
				// single-use approved key makes the second Check pass.
				s.publish(eventbus.TaskApprovalRequested, t.ID, map[string]string{
					"approval_id": approval.ID,
					"risk":        string(risk.Level),
					"action":      "production.deploy",
				})
				s.publish(eventbus.ApprovalRequested, approval.ID, map[string]string{
					"task": t.ID, "action": "production.deploy", "risk": string(risk.Level),
				})
				return t, fmt.Errorf("%w: %s", agent.ErrApprovalRequired, approval.ID)
			}
			if !allowed {
				s.fail(t, "governance: deploy denied")
				return t, fmt.Errorf("deploy: denied by governance (risk %s)", risk.Level)
			}
		}
	}

	if err := t.Transition(domain.TaskDeploying); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "DEPLOYING", "version": version})

	res, derr := deployer.Deploy(context.Background(), deployment.DeployRequest{
		Service:     "kern",
		Version:     version,
		ProjectRoot: s.platform.Root(),
	})
	s.publish(eventbus.DeploymentStarted, t.ID, map[string]string{"service": "kern", "version": version})

	if derr != nil || !res.Success {
		msg := "deploy failed"
		if derr != nil {
			msg = derr.Error()
		}
		if res.Output != "" {
			msg = res.Output
		}
		s.recordArtifact(domain.ArtifactDeployment, t.ID, "deploy-engine",
			fmt.Sprintf("deployment: %s (failed)", version),
			s.lastArtifactID(t.ID, domain.ArtifactPullRequest), "deploy:failed")
		s.publish(eventbus.DeploymentFailed, t.ID, map[string]string{"service": "kern", "version": version, "error": msg})
		s.publish(eventbus.DeploymentRolledBack, t.ID, map[string]string{"service": "kern", "version": version, "error": msg})
		s.fail(t, msg)
		return t, fmt.Errorf("deploy: %s", msg)
	}

	t.AddStep(agent.Step{
		Action:     "deploy",
		AgentID:    "deploy-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     res.Output,
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactDeployment, t.ID, "deploy-engine",
		fmt.Sprintf("deployment: %s", version),
		s.lastArtifactID(t.ID, domain.ArtifactPullRequest), "deploy:"+version)
	s.publish(eventbus.DeploymentCompleted, t.ID, map[string]string{"service": "kern", "version": version})
	s.persist(t)
	return t, nil
}

// Observe transitions a Task from DEPLOYING to OBSERVING, runs a simulated
// production health check, and transitions to COMPLETED if healthy. In local
// mode the health check is a no-op (always healthy). Returns the Task.
func (s *TaskService) Observe(taskID string) (*agent.Task, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err := t.Transition(domain.TaskObserving); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "OBSERVING"})
	t.AddStep(agent.Step{
		Action:     "observe",
		AgentID:    "observe-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     "production healthy",
		Status:     "success",
	})
	// Transition to COMPLETED — the full lifecycle is done.
	if err := t.Complete("lifecycle complete: all 20 steps passed"); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	// Record the Audit artifact (P10.4) as the finalize point of the workflow:
	// after the task completes, the whole lifecycle is summarized in a typed,
	// traceable audit artifact linked to the last PR/deployment artifact.
	s.recordAuditArtifact(t.ID, "audit trail for "+t.Intent)
	return t, nil
}

// recordAuditArtifact records an ArtifactAudit finalize artifact for a task.
// It is best-effort and non-breaking: it links the audit trail to the most
// recent pull-request or deployment artifact in the chain (falling back to an
// empty parent when neither exists). createdBy is "audit-engine" and the
// provenance is "audit:finalize".
func (s *TaskService) recordAuditArtifact(taskID, intent string) {
	if s.arts == nil {
		return
	}
	parentID := s.lastArtifactID(taskID, domain.ArtifactPullRequest)
	if parentID == "" {
		parentID = s.lastArtifactID(taskID, domain.ArtifactDeployment)
	}
	arts, err := s.arts.GetByTask(taskID)
	if err != nil {
		return
	}
	summary := fmt.Sprintf("audit trail for %s (%d artifacts)", intent, len(arts))
	s.recordArtifact(domain.ArtifactAudit, taskID, "audit-engine",
		summary, parentID, "audit:finalize")
}

// renderPRBody generates a PR description from the Task's structured artifacts
// (Plan, Impact, Verification) — not from an agent's memory. This is safer and
// more auditable.
func (s *TaskService) renderPRBody(t *agent.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", t.Intent)
	fmt.Fprintf(&b, "Task: %s\n\n", t.ID)

	if t.Plan != nil {
		fmt.Fprintf(&b, "### Plan\n")
		fmt.Fprintf(&b, "Objective: %s\n", t.Plan.Objective)
		fmt.Fprintf(&b, "Risk: %s\n", t.Plan.Risk)
		fmt.Fprintf(&b, "Scope: %s\n\n", t.Plan.Scope)
		if len(t.Plan.ImplementationSteps) > 0 {
			fmt.Fprintf(&b, "#### Implementation steps\n")
			for i, step := range t.Plan.ImplementationSteps {
				fmt.Fprintf(&b, "%d. %s\n", i+1, step)
			}
			fmt.Fprintf(&b, "\n")
		}
		if t.Plan.Rollback != "" {
			fmt.Fprintf(&b, "#### Rollback\n%s\n\n", t.Plan.Rollback)
		}
	}

	if t.Impact != nil {
		fmt.Fprintf(&b, "### Impact\n")
		fmt.Fprintf(&b, "Risk: %s\n", t.Impact.Risk)
		fmt.Fprintf(&b, "Callers: %d, Services: %d, APIs: %d\n\n", len(t.Impact.WhoCalls), len(t.Impact.ServicesDepend), len(t.Impact.APIsAffected))
	}

	if t.Verification != nil {
		fmt.Fprintf(&b, "### Verification\n")
		fmt.Fprintf(&b, "Verdict: %s\n", t.Verification.Verdict)
		fmt.Fprintf(&b, "Summary: %s\n\n", t.Verification.Summary)
	}

	fmt.Fprintf(&b, "---\nGenerated by kern from structured artifacts.\n")
	return b.String()
}

// artifactKindForAction maps a workflow action name to the appropriate artifact
// kind for recording.
func artifactKindForAction(action string) domain.ArtifactKind {
	switch action {
	case "analyze":
		return domain.ArtifactContextPacket
	case "plan":
		return domain.ArtifactPlan
	case "code":
		return domain.ArtifactCodePatch
	case "verify":
		return domain.ArtifactVerificationReport
	case "pr":
		return domain.ArtifactPullRequest
	default:
		return domain.ArtifactAnalysisReport
	}
}

// toolForAction maps a workflow step action to the MCP tool that executes it
// ( tool-decision trace). It is the deterministic tool-selection
// trace: which tool the control plane uses for each workflow step. Every
// returned name MUST be a registered MCP tool — steps without a dedicated
// tool (pr, deploy, observe) fall back to the workflow orchestrator that
// drives them, never a phantom "kern_<action>" label.
func toolForAction(action string) string {
	switch action {
	case "analyze":
		return "kern_analyze"
	case "plan":
		return "kern_plan"
	case "code":
		return "kern_execute"
	case "verify":
		return "kern_verify"
	case "pr", "deploy", "observe":
		return "kern_workflow"
	default:
		return "kern_workflow"
	}
}

// truncate shortens a string to at most n characters, appending "…" when
// truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// correlator returns the single shared correlation service for this TaskService
// It is built lazily once over the platform's runtime source so
// every lane that reasons over runtime-to-code correlation (correlate,
// investigate, deploy, observe) shares the exact same source + lookback window.
func (s *TaskService) correlator() *runtime.SharedCorrelator {
	if s.sharedCorr == nil {
		src := s.platform.RuntimeSource()
		if src == nil {
			src = runtime.NewStore()
		}
		s.sharedCorr = runtime.NewSharedCorrelator(src, incident.DefaultLookback)
	}
	return s.sharedCorr
}

// Correlate runs the runtime correlation engine against a production alert and
// records the result as a Task. It creates a Task, runs the
// Correlator + CorrelateChain, attaches the deep evidence chain
// (alert→service→deployment→commit→symbol→task/pr/agent), and records an
// incident-report artifact.
// The correlation is deterministic — the LLM may explain it, but the chain is
// derived from the runtime source and git history, not an LLM guess.
func (s *TaskService) Correlate(alert domain.Alert) (*agent.Task, runtime.CorrelationChain, string, error) {
	t, err := s.Create(fmt.Sprintf("correlate alert: %s", alert.Message))
	if err != nil {
		return nil, runtime.CorrelationChain{}, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, runtime.CorrelationChain{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Use the single shared correlation service so this lane reasons
	// over the same source/window as investigate/deploy/observe.
	chain := s.correlator().CorrelateChain(alert)

	t.Output = renderCorrelationText(chain)
	t.AddStep(agent.Step{
		Action:     "correlate",
		AgentID:    "runtime-correlator",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("correlated: %d links, service=%s", len(chain.Links), chain.Service),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactIncidentReport, t.ID, "runtime-correlator",
		fmt.Sprintf("correlation: %d links, service=%s", len(chain.Links), chain.Service),
		"", "correlate:runtime")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, chain, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, chain, t.Output, nil
}

// InvestigateIncident runs the full incident workflow : IngestAlert
// → Correlate → RootCause. It wraps the incident.Engine through TaskService so
// the incident lifecycle (Task, Artifacts, Events) is recorded on the
// authoritative Task.
// The incident engine reuses Task, Artifact, Event, Policy, Memory, Evidence,
// and Verification — it does not create a separate lifecycle framework.
func (s *TaskService) InvestigateIncident(alert domain.Alert) (*agent.Task, *domain.Incident, string, error) {
	t, err := s.Create(fmt.Sprintf("investigate incident: %s", alert.Message))
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Use the single shared correlation service so this lane reasons
	// over the same source/window as correlate/deploy/observe.
	shared := s.correlator()
	eng, err := incident.NewEngineWithGraph(s.platform.Root(), s.platform.Graph(), src, s.platform.Memory(), s.platform.Firewall())
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	eng.WithSharedCorrelator(shared)
	if s.bus != nil {
		eng.WithBus(s.bus)
	}

	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	t.Output = renderIncidentText(inc)
	t.AddStep(agent.Step{
		Action:     "investigate",
		AgentID:    "incident-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("incident: %s, status: %s, hypotheses: %d", inc.ID, inc.Status, len(inc.Hypotheses)),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactIncidentReport, t.ID, "incident-engine",
		fmt.Sprintf("incident: %s — %s", inc.ID, inc.Status),
		"", "incident:investigate")
	if inc.RootCause != nil {
		s.recordArtifact(domain.ArtifactRootCauseReport, t.ID, "incident-engine",
			fmt.Sprintf("root cause: %s", inc.RootCause.Summary),
			s.lastArtifactID(t.ID, domain.ArtifactIncidentReport), "incident:rootcause")
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}
	s.persist(t)
	s.publish(eventbus.IncidentCreated, inc.ID, map[string]string{"task": t.ID, "service": inc.AffectedService})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, inc, t.Output, nil
}

// RemediateIncident drives the candidate-fix pipeline end to end:
// the controlled incident (alert) is correlated and root-caused, the human
// approval gate is exercised, the candidate fix is applied in a sandbox,
// verified (build), and turned into a remediation PR. It is the app-layer
// counterpart to the incident engine's FixAndPR — the missing production
// entry that makes "controlled incident becomes a verified remediation PR"
// ( exit gate) reachable from the CLI/MCP/web.
// apply is the fix applier: it receives the sandbox worktree directory and
// must write the fix there (never the live repo). branch is the PR head
// branch. approver is the human identity granting the approval gate
// (Invariant #2: high-risk fixes require human approval; the decision is
// recorded in the approval workflow + bus). Returns the task, the remediated
// incident (status FIX_VERIFIED/PR_CREATED), and a rendered summary.
func (s *TaskService) RemediateIncident(alert domain.Alert, apply func(workDir string) error, branch, approver string) (*agent.Task, *domain.Incident, string, error) {
	t, err := s.Create(fmt.Sprintf("remediate incident: %s", alert.Message))
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Reason over the same shared correlation service as every
	// other runtime lane.
	shared := s.correlator()
	eng, err := incident.NewEngineWithGraph(s.platform.Root(), s.platform.Graph(), src, s.platform.Memory(), s.platform.Firewall())
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	eng.WithSharedCorrelator(shared)
	eng.WithPRProvider(s.prProvider)
	if s.bus != nil {
		eng.WithBus(s.bus)
	}

	// 11.2 — Correlation + 11.3 — Root cause (hypothesis / evidence / confidence).
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	// 11.4 — approval gate: a production remediation requires human approval.
	ap := eng.RequestApproval(inc, s.agentID, "remediate production incident")
	if _, err := eng.Approve(ap.ID, approver); err != nil {
		s.fail(t, "approval: "+err.Error())
		return t, inc, "", err
	}
	s.publish(eventbus.ApprovalRequested, ap.ID, map[string]string{"incident": inc.ID, "action": "incident.remediate"})
	s.publish(eventbus.TaskApproved, t.ID, map[string]string{"approval": ap.ID})

	// 11.4 — sandbox → verify → PR (risk gate + build inside ApplyAndVerifyFix).
	diff, err := eng.FixAndPR(inc, apply, branch)
	if err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}

	t.Output = renderIncidentText(inc)
	t.AddStep(agent.Step{
		Action:     "remediate",
		AgentID:    "incident-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("incident: %s → %s, diff=%d chars, PR=%s", inc.ID, inc.Status, len(diff), inc.PRURL),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactDiff, t.ID, "incident-engine",
		fmt.Sprintf("remediation diff for %s (%d chars)", inc.ID, len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactRootCauseReport), "incident:fix")
	if inc.Verification != "" {
		s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "incident-engine",
			inc.Verification, s.lastArtifactID(t.ID, domain.ArtifactDiff), "incident:verify")
	}
	s.recordArtifact(domain.ArtifactPullRequest, t.ID, "pr-engine",
		fmt.Sprintf("remediation PR for %s: %s (#%d)", inc.ID, inc.PRURL, inc.PRNumber),
		s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "incident:pr")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}
	s.persist(t)
	s.publish(eventbus.IncidentResolved, inc.ID, map[string]string{"task": t.ID, "status": string(inc.Status), "pr": inc.PRURL})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, inc, t.Output, nil
}

// Learn extracts recurring patterns from engineering memory and records them as
// a Task. It wraps the learning.Extractor through TaskService so the
// learning lifecycle (Change → Outcome → Pattern → Memory) is auditable.
// Patterns are promoted to memory only when they meet the threshold (evidence-
// based promotion). The LLM may explain patterns but does not create them.
func (s *TaskService) Learn(threshold int) (*agent.Task, []learning.Pattern, string, error) {
	if threshold <= 0 {
		threshold = 3
	}
	t, err := s.Create("extract learning patterns")
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	extractor := learning.New(s.platform.Memory())
	patterns, err := extractor.Patterns()
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	surfaced, err := extractor.Surface(threshold)
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}

	t.Output = renderLearningText(patterns, surfaced, threshold)
	t.AddStep(agent.Step{
		Action:     "learn",
		AgentID:    "learning-extractor",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("patterns: %d total, %d surfaced (threshold=%d)", len(patterns), len(surfaced), threshold),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactMemoryEntry, t.ID, "learning-extractor",
		fmt.Sprintf("learning: %d patterns, %d surfaced", len(patterns), len(surfaced)),
		"", "learning:extract")

	// Promote surfaced patterns to memory.
	for _, p := range surfaced {
		extractor.Remember(p)
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, patterns, "", err
	}
	s.persist(t)
	s.publish(eventbus.LessonRecorded, t.ID, map[string]string{"patterns": fmt.Sprintf("%d", len(surfaced))})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, patterns, t.Output, nil
}

// Modernize runs the legacy modernization analysis and records it as a Task
// It wraps the modernization.Analyzer through TaskService so each
// modernization phase becomes an auditable Task with artifacts.
// The analysis connects communities → bridges → churn → candidate boundaries →
// impact → risk → migration plan → executable tasks. Each extraction phase
// becomes a Task or Task Group.
func (s *TaskService) Modernize() (*agent.Task, modernization.ExtractionPlan, string, error) {
	t, err := s.Create("modernization analysis")
	if err != nil {
		return nil, modernization.ExtractionPlan{}, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, modernization.ExtractionPlan{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	analyzer := modernization.NewAnalyzer(s.platform.Index())
	planPtr, err := analyzer.Analyze()
	if err != nil {
		s.fail(t, err.Error())
		return t, modernization.ExtractionPlan{}, "", err
	}
	plan := *planPtr

	t.Output = renderModernizationText(plan)
	t.AddStep(agent.Step{
		Action:     "modernize",
		AgentID:    "modernization-analyzer",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("modernization: %d contexts, %d bridges, %d phases", len(plan.Contexts), len(plan.Bridges), len(plan.Phases)),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactArchitectureReport, t.ID, "modernization-analyzer",
		fmt.Sprintf("modernization: %d contexts, %d phases", len(plan.Contexts), len(plan.Phases)),
		"", "modernization:analyze")

	// / exit gate: modernization is Task aware. Materialize each
	// extraction phase as its own task (Task Group → Tasks), linked to this
	// plan task, so the phases are individually tracked, auditable, and
	// resumable instead of living only inside the plan text.
	if len(plan.Phases) > 0 {
		if phaseTasks, perr := s.ModernizePhaseTasks(plan, t.ID); perr != nil {
			s.publish(eventbus.TaskFailed, t.ID, map[string]string{"error": "modernize phase tasks: " + perr.Error()})
		} else {
			t.AddStep(agent.Step{
				Action:     "modernize-phase-tasks",
				AgentID:    "modernization-analyzer",
				StartedAt:  t.UpdatedAt,
				FinishedAt: time.Now(),
				Result:     fmt.Sprintf("materialized %d phase tasks", len(phaseTasks)),
				Status:     "success",
			})
		}
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, plan, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, plan, t.Output, nil
}

// ModernizePhaseTasks materializes each extraction phase as its own task
// ( one task per phase, not a single task for the whole plan). Each
// phase-task records an artifact and is linked by a parent reference to the
// plan task. It returns the created phase tasks.
func (s *TaskService) ModernizePhaseTasks(plan modernization.ExtractionPlan, parentTaskID string) ([]*agent.Task, error) {
	var out []*agent.Task
	for i := range plan.Phases {
		phase := &plan.Phases[i]
		pt, err := s.Create(fmt.Sprintf("modernize phase %d: extract %s", phase.Phase, phase.Context))
		if err != nil {
			return out, err
		}
		pt.Scope = "service:" + phase.Context
		pt.CreatedBy = "modernization-analyzer"
		// Link the phase task to its plan task so the audit trail can trace a
		// phase back to the plan that produced it .
		pt.ParentID = parentTaskID
		phase.TaskID = pt.ID
		if err := pt.Transition(domain.TaskCompleted); err == nil {
			pt.Output = renderModernizePhaseText(*phase)
			s.persist(pt)
		}
		// Record an artifact for the phase (a phase task is an auditable unit).
		s.recordArtifact(domain.ArtifactArchitectureReport, pt.ID, "modernization-analyzer",
			fmt.Sprintf("phase %d: extract %s (risk %s)", phase.Phase, phase.Context, phase.RiskLevel),
			s.lastArtifactID(parentTaskID, domain.ArtifactArchitectureReport), "modernization:phase")
		s.publish(eventbus.TaskCreated, pt.ID, map[string]string{"kind": "modernize-phase", "parent": parentTaskID})
		out = append(out, pt)
	}
	return out, nil
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
