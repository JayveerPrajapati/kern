// Package agents defines dynamic, task-type-driven agent selection for the
// multi-agent pipeline. Instead of always invoking every agent, a task is
// classified into a kind (code change, documentation, incident, modernization)
// and a matching pipeline (stage sequence) is selected. Classification is
// deterministic keyword matching — no LLM involved.
package agents

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TaskKind classifies a task so the caller can compose an appropriate team
// pipeline rather than always running the full 6-stage sequence.
type TaskKind int

const (
	// TaskKindCode is a regular code change; it uses the full default pipeline.
	TaskKindCode TaskKind = iota
	// TaskKindDocumentation is a documentation-only change (Planner → Reviewer).
	TaskKindDocumentation
	// TaskKindIncident is an incident/root-cause task (Planner → Coder →
	// Security → Tester → SRE).
	TaskKindIncident
	// TaskKindModernization is a refactor/modernization task (Architect →
	// Planner → Reviewer).
	TaskKindModernization
	// TaskKindDefault is the backward-compatible default (the current 6-stage
	// sequence). It is kept distinct from TaskKindCode so callers can opt into
	// the full default without being classified.
	TaskKindDefault
)

// SelectPipeline returns the ordered stage sequence for a task kind. Each
// variant maps directly to the plan's "AGENT SELECTION" section. TaskKindCode
// and TaskKindDefault both resolve to the current default 6-stage sequence so
// no v1 behavior changes.
func SelectPipeline(kind TaskKind) []stageSpec {
	switch kind {
	case TaskKindDocumentation:
		return []stageSpec{
			{name: "plan", role: RolePlanner, action: "plan"},
			{name: "review", role: RoleReviewer, action: "review"},
		}
	case TaskKindIncident:
		return []stageSpec{
			{name: "plan", role: RolePlanner, action: "plan"},
			{name: "code", role: RoleCoder, action: "code"},
			{name: "security", role: RoleSecurity, action: "security"},
			{name: "test", role: RoleTester, action: "test"},
			{name: "sre", role: RoleSRE, action: "sre"},
		}
	case TaskKindModernization:
		return []stageSpec{
			{name: "architect", role: RoleArchitect, action: "architect"},
			{name: "plan", role: RolePlanner, action: "plan"},
			{name: "review", role: RoleReviewer, action: "review"},
		}
	default:
		// TaskKindCode and TaskKindDefault both run the full pipeline.
		return DefaultStages()
	}
}

// ClassifyTask heuristically maps an intent (task description) and an optional
// task type to a [TaskKind]. It is deterministic keyword matching — no LLM.
// An explicit taskType wins over intent keyword scanning, and the default is
// TaskKindCode (the full pipeline), so unrecognized tasks keep v1 behavior.
func ClassifyTask(intent, taskType string) TaskKind {
	t := strings.ToLower(strings.TrimSpace(taskType))
	i := strings.ToLower(intent)

	if t == "incident" || containsAny(i, "incident", "correlate", "root-cause", "alert") {
		return TaskKindIncident
	}
	if t == "modernize" || containsAny(i, "modernize", "refactor", "extract", "split-monolith") {
		return TaskKindModernization
	}
	if t == "documentation" || containsAny(i, "document", "docs", "readme") {
		return TaskKindDocumentation
	}
	return TaskKindCode
}

// PipelineForKind builds a [Pipeline] whose stages come from [SelectPipeline]
// for the given kind instead of the hardcoded standard stages. Nil team,
// runtime, or approval workflow are replaced with fresh defaults. The Pipeline
// does NOT insert a human approval gate — callers that need governance must
// use [SelectWorkflow] with the WorkflowEngine instead.
func PipelineForKind(kind TaskKind, team *SpecialistRegistry, runtime *agent.Registry, approvals *governance.ApprovalWorkflow) *Pipeline {
	return NewPipelineWithStages(team, runtime, approvals, SelectPipeline(kind))
}

// SelectWorkflow returns a task-kind-specific [agent.Workflow] that preserves
// the human approval gate (the "approve" step with RequiresApproval) before the
// first execution step. This is the governance-preserving counterpart to
// [SelectPipeline]: SelectPipeline is for direct pipeline callers that handle
// approval externally; SelectWorkflow is for the WorkflowEngine path used by
// TaskService.RunWorkflow, where Invariant #2 (high-risk execution requires
// approval) must hold.
func SelectWorkflow(kind TaskKind) agent.Workflow {
	switch kind {
	case TaskKindDocumentation:
		return agent.Workflow{
			ID:   "documentation",
			Name: "Documentation workflow",
			Steps: []agent.WorkflowStep{
				{Action: "plan", AgentType: "planner"},
				{Action: "approve", AgentType: "human", RequiresApproval: true},
				{Action: "review", AgentType: "reviewer"},
				{Action: "pr", AgentType: "reviewer"},
			},
		}
	case TaskKindIncident:
		return agent.Workflow{
			ID:   "incident",
			Name: "Incident workflow",
			Steps: []agent.WorkflowStep{
				{Action: "plan", AgentType: "planner"},
				{Action: "approve", AgentType: "human", RequiresApproval: true},
				{Action: "code", AgentType: "coder"},
				{Action: "security", AgentType: "security"},
				{Action: "test", AgentType: "tester"},
				{Action: "sre", AgentType: "sre"},
				{Action: "pr", AgentType: "reviewer"},
			},
		}
	case TaskKindModernization:
		return agent.Workflow{
			ID:   "modernization",
			Name: "Modernization workflow",
			Steps: []agent.WorkflowStep{
				{Action: "architect", AgentType: "architect"},
				{Action: "plan", AgentType: "planner"},
				{Action: "approve", AgentType: "human", RequiresApproval: true},
				{Action: "review", AgentType: "reviewer"},
				{Action: "pr", AgentType: "reviewer"},
			},
		}
	default:
		// TaskKindCode and TaskKindDefault use the standard 7-step workflow
		// (request→analyze→plan→approve→code→verify→pr).
		return agent.DefaultWorkflow()
	}
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}