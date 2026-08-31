package agents

import (
	"errors"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// stageSpec describes one stage of the multi-agent pipeline: the role that
// executes it, the action passed to the step handler, and the stage name used
// in StageResult.
type stageSpec struct {
	name   string
	role   Role
	action string
}

// standardStages is the workflow order: Planner → Architect → Coder
// → Reviewer → Security → Tester.
var standardStages = []stageSpec{
	{name: "plan", role: RolePlanner, action: "plan"},
	{name: "architect", role: RoleArchitect, action: "architect"},
	{name: "code", role: RoleCoder, action: "code"},
	{name: "review", role: RoleReviewer, action: "review"},
	{name: "security", role: RoleSecurity, action: "security"},
	{name: "test", role: RoleTester, action: "test"},
}

// DefaultStages returns the current standard 6-stage sequence (a copy), so
// existing callers and the default task kind keep their behavior.
func DefaultStages() []stageSpec {
	return append([]stageSpec{}, standardStages...)
}

// StageResult records one stage's outcome.
type StageResult struct {
	Stage      string // "plan", "architect", "code", "review", "security", "test"
	Specialist string // specialist identity
	Output     string
	OK         bool
}

// Pipeline runs a task through the specialist team, handing the task forward
// between stages and invoking the step handler per stage. The caller decides
// how each specialist executes; the pipeline records a StageResult per stage.
// The stage sequence is chosen at construction (default: the 6-stage
// [DefaultStages]) so callers can compose kind-specific pipelines.
type Pipeline struct {
	team      *SpecialistRegistry
	runtime   *agent.Registry // agent runtime registry
	approvals *governance.ApprovalWorkflow
	stages    []stageSpec   // ordered stages to run; nil means the default
	bus       *eventbus.Bus // optional event publisher; nil = no-op
}

// NewPipeline creates a pipeline with the standard 6-stage sequence; nil team,
// runtime, or approval workflow are replaced with fresh ones. Specialists are
// resolved from the team. Use [NewPipelineWithStages] to select a custom stage
// sequence.
func NewPipeline(team *SpecialistRegistry, runtime *agent.Registry, approvals *governance.ApprovalWorkflow) *Pipeline {
	return NewPipelineWithStages(team, runtime, approvals, DefaultStages())
}

// NewPipelineWithStages creates a pipeline running the given stages in order.
// An empty stages slice falls back to the default 6-stage sequence. Nil team,
// runtime, or approval workflow are replaced with fresh ones.
func NewPipelineWithStages(team *SpecialistRegistry, runtime *agent.Registry, approvals *governance.ApprovalWorkflow, stages []stageSpec) *Pipeline {
	if team == nil {
		team = NewSpecialistRegistry()
	}
	if runtime == nil {
		runtime = agent.NewRegistry()
	}
	if approvals == nil {
		approvals = governance.NewApprovalWorkflow()
	}
	if len(stages) == 0 {
		stages = DefaultStages()
	}
	return &Pipeline{team: team, runtime: runtime, approvals: approvals, stages: stages}
}

// WithBus attaches an optional event bus; a nil bus is a no-op.
func (p *Pipeline) WithBus(b *eventbus.Bus) *Pipeline {
	p.bus = b
	return p
}

// publish delivers an agent event to the optional bus.
func (p *Pipeline) publish(ev eventbus.Event) {
	if p.bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = "agents"
	}
	p.bus.Publish(ev)
}

// Run executes a task through the full pipeline. The stepHandler signature is
// func(action string, specialist *Specialist, task *agent.Task) (string, error)
// where action is the stage action and specialist the resolved specialist for
// the stage. Run returns the (possibly partial) task, per-stage results, and an
// error: it stops when a stage has no specialist or the handler errors. A
// handler error wrapping [agent.ErrApprovalRequired] stops the pipeline for the
// caller to resolve approval and re-run. The task is handed off between stages
// via a fresh [agent.HandoffManager].
func (p *Pipeline) Run(task *agent.Task, stepHandler func(action string, specialist *Specialist, task *agent.Task) (string, error)) (*agent.Task, []StageResult, error) {
	if task == nil {
		return nil, nil, errors.New("agents: cannot run a nil task")
	}
	if stepHandler == nil {
		return task, nil, errors.New("agents: cannot run with a nil step handler")
	}

	handoffs := agent.NewHandoffManager()
	results := make([]StageResult, 0, len(p.stages))
	var previous string

	for _, stage := range p.stages {
		specialists := p.team.ByRole(stage.role)
		if len(specialists) == 0 {
			return task, results, fmt.Errorf("agents: no %q specialist for stage %q", stage.role, stage.name)
		}
		spec := specialists[0]

		// Hand off the task from the previous stage's specialist.
		if previous != "" && previous != spec.Agent.ID {
			handoffs.Handoff(task.ID, previous, spec.Agent.ID, stage.name)
			p.publish(eventbus.Event{
				Kind:    eventbus.AgentHandoff,
				Subject: task.ID,
				Payload: map[string]string{"from": previous, "to": spec.Agent.ID, "stage": stage.name},
			})
		}

		task.AgentID = spec.Agent.ID
		out, err := stepHandler(stage.action, spec, task)
		if err != nil {
			if errors.Is(err, agent.ErrApprovalRequired) {
				return task, results, err
			}
			results = append(results, StageResult{Stage: stage.name, Specialist: spec.Agent.ID, Output: err.Error(), OK: false})
			p.publish(eventbus.Event{
				Kind:    eventbus.AgentError,
				Subject: task.ID,
				Payload: map[string]string{"stage": stage.name, "agent": spec.Agent.ID, "error": err.Error()},
			})
			p.publish(eventbus.Event{
				Kind:    eventbus.AgentFailed,
				Subject: task.ID,
				Payload: map[string]string{"stage": stage.name, "agent": spec.Agent.ID, "error": err.Error()},
			})
			return task, results, err
		}
		task.AddStep(agent.Step{Action: stage.action, AgentID: spec.Agent.ID, Result: out, Status: "success"})
		results = append(results, StageResult{Stage: stage.name, Specialist: spec.Agent.ID, Output: out, OK: true})
		p.publish(eventbus.Event{
			Kind:    eventbus.AgentToolCalled,
			Subject: task.ID,
			Payload: map[string]string{"stage": stage.name, "agent": spec.Agent.ID, "action": stage.action},
		})
		p.publish(eventbus.Event{
			Kind:    eventbus.AgentCompleted,
			Subject: task.ID,
			Payload: map[string]string{"stage": stage.name, "agent": spec.Agent.ID},
		})
		previous = spec.Agent.ID
	}

	return task, results, nil
}
