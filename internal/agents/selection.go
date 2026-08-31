// Package agents defines dynamic, task-type-driven agent selection for the
// multi-agent pipeline. Instead of always invoking every agent, a task is
// classified into a kind (code change, documentation, incident, modernization)
// and a matching pipeline (stage sequence) is selected. Classification is
// deterministic keyword matching — no LLM involved.
package agents

import (
	"sort"
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

// RoutingContext carries the dynamic routing inputs that influence
// which agent roles execute a task. It extends the basic intent/task-type
// classification with repository context, policy constraints, and per-role
// historical success so routing can honor the full set of inputs described in
// the plan. All routing decisions derived from it are deterministic — no LLM,
// no network, no map-iteration nondeterminism.
// HistoricalSuccess maps an agent/role ID to its historical success rate in
// the range [0,1]. Roles with a higher rate are preferred, everything else
// being equal.
type RoutingContext struct {
	// Intent is the free-form task description (same input as ClassifyTask).
	Intent string
	// TaskType is an optional explicit task type, e.g. "incident", "modernize".
	TaskType string
	// Repository is the target repository/service name (may be empty).
	Repository string
	// Language is the dominant language of the change (informational today).
	Language string
	// Policy is the governance policy label, e.g. "governed", "high-risk",
	// "production", "sandbox-only" (may be empty). It biases which roles run.
	Policy string
	// HistoricalSuccess maps agent/role ID -> success rate in [0,1]. Higher is
	// preferred. May be nil.
	HistoricalSuccess map[string]float64
}

// Kind returns the [TaskKind] for this routing context by delegating to
// [ClassifyTask], preserving backward compatibility with the existing
// classification entry point.
func (r RoutingContext) Kind() TaskKind {
	return ClassifyTask(r.Intent, r.TaskType)
}

// RankRoles returns the candidate agent roles sorted by suitability for this
// routing context. Ranking is deterministic and considers, in order of bias:
// - repository match: a candidate role whose lowercase name appears as a
// substring of the (lowercased) repository is boosted;
// - policy match: if Policy is non-empty, roles implied by the policy (e.g.
// "security" for "high-risk", "sre" for "production") are boosted;
// - historical success: candidates present in HistoricalSuccess are boosted
// by their success rate, so higher-success roles rank higher.
// The result is ordered score-descending, with alphabetical order as the
// tie-breaker, so the output is fully deterministic regardless of input map
// iteration order.
func (r RoutingContext) RankRoles(candidates []string) []string {
	type scored struct {
		role  string
		score float64
	}

	repo := strings.ToLower(r.Repository)
	policyRoles := policyRolesFor(strings.ToLower(strings.TrimSpace(r.Policy)))

	list := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		role := strings.ToLower(c)
		score := 0.0

		// Repository bias: the role's name appears inside the repository.
		if repo != "" && strings.Contains(repo, role) {
			score += 1.0
		}

		// Policy bias: role is implied by the active policy.
		for _, pr := range policyRoles {
			if pr == role {
				score += 2.0
			}
		}

		// Historical success: prefer roles that have performed well.
		if v, ok := r.HistoricalSuccess[c]; ok && v > 0 {
			score += v
		}

		list = append(list, scored{role: c, score: score})
	}

	// Stable sort: score desc, then alphabetical. Stable sort plus an
	// alphabetical tie-breaker makes the result deterministic.
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].role < list[j].role
	})

	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.role)
	}
	return out
}

// RouteTeam returns the ordered agent roles for this routing context. It
// starts from the pipeline roles selected for the classified [TaskKind] and
// reorders them by [RoutingContext.RankRoles] so repository, policy, and
// historical-success influences are honored where they apply. It never returns
// an empty slice: if ranking yields nothing useful it falls back to the
// pipeline roles in their natural order.
func (r *RoutingContext) RouteTeam() []string {
	return r.route()
}

// RouteFor is an alias for [RoutingContext.RouteTeam]; both produce the same
// deterministic, policy/repository/history-aware role ordering for this
// routing context.
func (r *RoutingContext) RouteFor() []string {
	return r.route()
}

// route is the shared implementation behind RouteTeam and RouteFor.
func (r *RoutingContext) route() []string {
	stages := SelectPipeline(r.Kind())
	if len(stages) == 0 {
		return []string{}
	}

	natural := make([]string, 0, len(stages))
	for _, s := range stages {
		natural = append(natural, string(s.role))
	}

	// Overlay the repository/policy/history ranking. RankRoles always returns
	// the same number of roles as its input, so this can only reorder the
	// pipeline set — it can never drop roles or produce an empty result.
	ranked := r.RankRoles(natural)
	if len(ranked) == 0 {
		return natural
	}
	return ranked
}

// policyRolesFor maps a (lowercased) policy label to the set of roles it
// biases toward. Unknown or empty policies return no roles so they do not
// affect ranking.
func policyRolesFor(policy string) []string {
	switch policy {
	case "high-risk", "high risk":
		return []string{"security"}
	case "production", "governed-prod", "prod":
		return []string{"sre"}
	case "governed":
		return []string{"planner", "reviewer"}
	case "sandbox-only", "sandbox":
		return []string{"tester", "reviewer"}
	default:
		return nil
	}
}
