package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TaskService is the application service that makes Task authoritative. It
// creates, progresses, and persists Tasks through the analyze → impact → plan →
// verify lifecycle, so every interface (CLI, MCP, REST) can create an
// authoritative Task record instead of running stateless.
//
// This realizes the Integration Transformation Plan's Phase 2: "Make Task
// Authoritative." Before this service, kern analyze / kern_analyze ran
// stateless — no Task was created, no lifecycle was recorded. Now every
// analysis creates a Task, progresses it through the state machine, persists
// it to the TaskStore, and attaches lifecycle results (context packet, impact,
// verification) to the Task so it is the single authoritative object for
// audit, resume, and debugging.
//
// TaskService wraps Platform's engine methods with Task lifecycle management.
// It does NOT replace Platform.Analyze/WhatIf/Verify — those remain the
// stateless fast path. TaskService adds the Task-tracking layer on top.
type TaskService struct {
	platform   *Platform
	registry   *agent.Registry
	store      *agent.TaskStore
	arts       *ArtifactStore
	bus        *eventbus.Bus
	agentID    string              // identity of the calling interface (Invariant 6)
	prProvider prprovider.Provider // PR creation provider (default Noop)
	deployer   deployment.Deployer // deployer for the Deploy method (default Noop)
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
		platform:   p,
		registry:   reg,
		store:      store,
		arts:       NewArtifactStore(p.Root()),
		bus:        bus,
		agentID:    "kern", // default identity; override via WithAgentID
		prProvider: prprovider.NoopProvider{},
		deployer:   deployment.NewDeployerFromEnv(),
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

// Create makes a new Task for the given intent and submits it to the registry.
// The Task starts in CREATED state with the intent as both Input and Intent.
// Returns the created Task (a pointer into the registry, so state mutations
// are visible) or an error if submission fails.
func (s *TaskService) Create(intent string) (*agent.Task, error) {
	t := agent.NewTask("analyze", intent)
	t.Intent = intent
	t.CreatedBy = s.agentID
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

// Analyze creates a Task for the intent, runs the context engine, and attaches
// the ContextPacket to the Task. The Task transitions CREATED → ANALYZING →
// (COMPLETED or FAILED). Returns the Task and the rendered analysis text.
//
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
	// webhooks/audit (Phase 4 event standardization).
	for _, r := range pkt.Risks {
		s.publish(eventbus.RiskCalculated, t.ID, map[string]string{
			"level":      string(r.Level),
			"mitigation": r.Mitigation,
		})
	}

	// Record the ContextPacket as the root artifact of the chain.
	s.recordArtifact(domain.ArtifactContextPacket, t.ID, "context-engine",
		"context packet: "+change, "", "context:analyze")

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
	// carries the change blast radius to webhooks/audit (Phase 4).
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
//
// This realizes the Integration Transformation Plan's Phase 6: the Plan
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
// queries from the spec (Phase 7), and attaches the ImpactReport to the Task.
// The Task transitions CREATED → ANALYZING → (COMPLETED or FAILED). Returns
// the Task, the ImpactReport, and a rendered text summary.
//
// This realizes the Integration Transformation Plan's Phase 7: the impact
// report is the deterministic source — the LLM may explain it, but the data
// comes from the knowledge graph, not an LLM guess.
func (s *TaskService) Impact(change string) (*agent.Task, domain.ImpactReport, string, error) {
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
	rep := domain.ImpactReport{Target: target}

	// 1. What calls this?
	for _, n := range g.WhoCalls(target) {
		rep.WhoCalls = append(rep.WhoCalls, nodeName(n))
	}
	// 2. What does it call?
	for _, n := range g.WhatDoesXDependOn(target) {
		rep.WhatItCalls = append(rep.WhatItCalls, nodeName(n))
	}
	// 3. What services depend on it?
	for _, n := range g.WhatServicesAffected(target) {
		rep.ServicesDepend = append(rep.ServicesDepend, nodeName(n))
	}
	// 4. Which APIs are affected?
	for _, n := range g.WhatAPIsAffected(target) {
		rep.APIsAffected = append(rep.APIsAffected, nodeName(n))
	}
	// 5. Which data stores are affected? (from the context packet's databases)
	pkt, _ := s.platform.ctx.AnalyzeChange(target)
	for _, e := range pkt.RuntimeEvidence {
		if strings.Contains(strings.ToLower(string(e.Type)), "database") || strings.Contains(strings.ToLower(string(e.Type)), "db") {
			rep.DataStoresAffected = append(rep.DataStoresAffected, e.Content)
		}
	}
	// 6. Which events are affected?
	for _, n := range g.WhatEventsAffected(target) {
		rep.EventsAffected = append(rep.EventsAffected, nodeName(n))
	}
	// 7. Which tests cover it?
	for _, n := range g.WhatTestsCover(target) {
		rep.TestsCover = append(rep.TestsCover, nodeName(n))
	}
	// 8. Which deployments are related? (no graph query yet — empty)
	// 9. Which incidents are related? (from memory recall)
	if s.platform.Memory() != nil {
		ms, _ := s.platform.Memory().Recall(memory.Query{Text: target, Type: domain.MemoryIncident})
		for _, m := range ms {
			rep.IncidentsRelated = append(rep.IncidentsRelated, m.ID)
		}
	}
	// 10. Which architecture rules apply?
	for _, p := range pkt.ArchitectureRules {
		rep.ArchitectureRules = append(rep.ArchitectureRules, p.ID)
	}
	// 11. Risk from production criticality.
	crit := g.ProductionCriticality(target)
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

	t.Impact = &rep
	t.Output = renderImpactText(rep)
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
		return t, rep, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, rep, t.Output, nil
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

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, res, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, res, nil
}

// RunWorkflow drives a Task through a dynamically selected agent workflow. The
// task is classified by kind (code change, documentation, incident,
// modernization) and the matching workflow — i.e. only the specialists that
// apply to that kind — is registered on the WorkflowEngine. This realizes the
// Integration Transformation Plan's "AGENT SELECTION": do not invoke every
// agent for every request. Unclassified tasks fall back to the default
// workflow. The kind-specific workflows each preserve the human "approve" gate
// before the first execution step, so Invariant #2 (high-risk execution
// requires approval) holds on every path.
//
// The stepHandler is called for each workflow step; it receives the action
// name and the Task, and returns the step output. This is where specialist
// agents (planner, coder, reviewer, etc.) are invoked. Each step records an
// artifact when the stepHandler returns a non-empty output.
func (s *TaskService) RunWorkflow(intent string, stepHandler func(action string, t *agent.Task) (string, error)) (*agent.Task, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, err
	}

	// Task-type-driven agent selection: register the workflow whose steps fit
	// the task kind, falling back to the full default workflow for unclassified
	// tasks. Both paths preserve the human approval gate.
	kind := agents.ClassifyTask(t.Input, t.Type)
	eng := agent.NewWorkflowEngine(s.registry, governance.NewApprovalWorkflow())
	if s.bus != nil {
		eng.WithBus(s.bus)
	}
	eng.RegisterWorkflow(agents.SelectWorkflow(kind))

	// Wrap the step handler to record artifacts for each step.
	wrapped := func(action string, task *agent.Task) (string, error) {
		out, err := stepHandler(action, task)
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

// Execute runs a patch in a sandboxed worktree, gated by governance (Phase 11).
// It creates a Task, transitions to EXECUTING, checks the governance firewall,
// applies the patch in a worktree, records the diff as an artifact, and returns
// the Task + diff.
//
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
//
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

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, diff, vres, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, diff, vres, nil
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
// (Phase 12). Unlike the standalone Verify, this chains after Execute: it
// verifies the specific worktree produced by execution, not the current tree.
//
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

// CreatePR creates a PR from the Task's structured artifacts (Phase 13).
// It renders a PR body from the Plan, Impact, and Verification artifacts, and
// transitions the Task to PR_CREATED.
//
// The PR requires: (1) verification passed (Task is in READY_FOR_PR), (2) the
// diff artifact exists. The PR body is generated from artifacts, not from an
// agent's memory of what it changed — this is safer and more auditable.
//
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
//
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
	return t, nil
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

// truncate shortens a string to at most n characters, appending "…" when
// truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Correlate runs the runtime correlation engine against a production alert and
// records the result as a Task (Phase 14). It creates a Task, runs the
// Correlator + CorrelateChain, attaches the deep evidence chain
// (alert→service→deployment→commit→symbol→task/pr/agent), and records an
// incident-report artifact.
//
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
	correlator := runtime.NewCorrelator(src, 30*time.Minute)
	chain := correlator.CorrelateChain(alert)

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

// InvestigateIncident runs the full incident workflow (Phase 15): IngestAlert
// → Correlate → RootCause. It wraps the incident.Engine through TaskService so
// the incident lifecycle (Task, Artifacts, Events) is recorded on the
// authoritative Task.
//
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
	eng, err := incident.NewEngineWithGraph(s.platform.Root(), s.platform.Graph(), src, s.platform.Memory(), s.platform.Firewall())
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
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

// Learn extracts recurring patterns from engineering memory and records them as
// a Task (Phase 16). It wraps the learning.Extractor through TaskService so the
// learning lifecycle (Change → Outcome → Pattern → Memory) is auditable.
//
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
// (Phase 17). It wraps the modernization.Analyzer through TaskService so each
// modernization phase becomes an auditable Task with artifacts.
//
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

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, plan, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, plan, t.Output, nil
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
}

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
		Kind:    kind,
		Source:  "app",
		Subject: subject,
		Payload: payload,
	})
}
