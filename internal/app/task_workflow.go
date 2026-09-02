// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"time"
)

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
