package app

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// CompileIntent classifies a raw intent string into a CompiledIntent with an
// IntentType, objective, target, scope, and environment. Strict Plan Phase 6 P0.
//
// Classification is deterministic (keyword/verb matching, no LLM):
//   - "explain"/"understand"/"what does" → UNDERSTAND
//   - "add"/"implement"/"fix"/"refactor"/"remove" → CODE_CHANGE
//   - "review"/"check" → REVIEW
//   - "what if"/"simulate"/"predict" → WHAT_IF
//   - "incident"/"alert"/"failing"/"down" → INCIDENT
//   - "modernize"/"split"/"extract" → MODERNIZATION
//   - "security"/"vulnerab"/"secret" → SECURITY
//   - "test" → TEST
//   - "deploy"/"release" → DEPLOY
//   - "audit"/"who changed"/"what did" → AUDIT
func CompileIntent(raw string) domain.CompiledIntent {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	it := domain.IntentCodeChange // default

	switch {
	case containsAny(lower, "explain", "understand", "what does", "how does", "describe"):
		it = domain.IntentUnderstand
	case containsAny(lower, "what if", "simulate", "predict", "impact of"):
		it = domain.IntentWhatIf
	case containsAny(lower, "deploy", "release", "rollout"):
		it = domain.IntentDeploy
	case containsAny(lower, "incident", "alert", "failing", "down", "outage", "production"):
		it = domain.IntentIncident
	case containsAny(lower, "modernize", "split", "extract service", "monolith"):
		it = domain.IntentModernization
	case containsAny(lower, "security", "vulnerab", "secret", "cve"):
		it = domain.IntentSecurity
	case containsAny(lower, "review", "check code", "inspect"):
		it = domain.IntentReview
	case containsAny(lower, "test", "coverage", "unit test"):
		it = domain.IntentTest
	case containsAny(lower, "audit", "who changed", "what did", "governance"):
		it = domain.IntentAudit
	case containsAny(lower, "add", "implement", "fix", "refactor", "remove", "update", "change", "modify"):
		it = domain.IntentCodeChange
	}

	target := extractTarget(raw)
	return domain.CompiledIntent{
		Type:           it,
		Objective:      raw,
		Target:         target,
		Scope:          "repository",
		Environment:    "development",
		DesiredOutcome: raw,
		RawText:        raw,
	}
}

// SelectWorkflow maps an IntentType to a primary workflow (A-E). Strict Plan
// Phase 6 P0.
func SelectWorkflow(it domain.IntentType) domain.WorkflowID {
	switch it {
	case domain.IntentUnderstand:
		return domain.WorkflowUnderstand
	case domain.IntentCodeChange, domain.IntentReview, domain.IntentTest, domain.IntentSecurity:
		return domain.WorkflowSafeChange
	case domain.IntentWhatIf, domain.IntentModernization:
		return domain.WorkflowPredict
	case domain.IntentIncident, domain.IntentDeploy:
		return domain.WorkflowOperate
	case domain.IntentAudit:
		return domain.WorkflowGovern
	default:
		return domain.WorkflowSafeChange
	}
}

// DefaultCapabilities returns the capabilities required for an intent type.
// Strict Plan Phase 6 P1: capability planner selects only the required
// capabilities.
func DefaultCapabilities(it domain.IntentType) []domain.Capability {
	switch it {
	case domain.IntentUnderstand:
		return []domain.Capability{
			{Name: "understand", Tools: []string{"kern_explore", "kern_graph", "kern_code_graph"}, Outputs: []string{"system context"}, Risk: "low"},
		}
	case domain.IntentCodeChange:
		return []domain.Capability{
			{Name: "analyze", Tools: []string{"kern_analyze"}, Outputs: []string{"context packet"}, Artifacts: []string{"ArtifactContextPacket"}, Risk: "low"},
			{Name: "plan", Tools: []string{"kern_plan"}, Outputs: []string{"plan"}, Artifacts: []string{"ArtifactPlan"}, Risk: "low"},
			{Name: "impact", Tools: []string{"kern_impact"}, Outputs: []string{"impact report"}, Artifacts: []string{"ArtifactImpactReport"}, Risk: "low"},
			{Name: "execute", Tools: []string{"kern_execute"}, Permissions: []string{"write"}, Artifacts: []string{"ArtifactCodePatch", "ArtifactDiff"}, Risk: "medium"},
			{Name: "verify", Tools: []string{"kern_verify"}, Outputs: []string{"verification"}, Artifacts: []string{"ArtifactVerificationReport"}, Risk: "low"},
			{Name: "pr", Tools: []string{"kern_execute"}, Outputs: []string{"pull request"}, Artifacts: []string{"ArtifactPullRequest"}, Risk: "medium"},
		}
	case domain.IntentWhatIf:
		return []domain.Capability{
			{Name: "whatif", Tools: []string{"kern_what_if"}, Outputs: []string{"impact", "recommendation"}, Risk: "low"},
		}
	case domain.IntentIncident:
		return []domain.Capability{
			{Name: "correlate", Tools: []string{"kern_correlate"}, Outputs: []string{"correlation chain"}, Risk: "low"},
			{Name: "investigate", Tools: []string{"kern_incident"}, Outputs: []string{"root cause"}, Artifacts: []string{"ArtifactIncidentReport", "ArtifactRootCauseReport"}, Risk: "medium"},
		}
	case domain.IntentModernization:
		return []domain.Capability{
			{Name: "modernize", Tools: []string{"kern_modernize"}, Outputs: []string{"modernization plan"}, Risk: "low"},
		}
	case domain.IntentSecurity:
		return []domain.Capability{
			{Name: "security", Tools: []string{"kern_security"}, Outputs: []string{"security report"}, Artifacts: []string{"ArtifactSecurityReport"}, Risk: "medium"},
		}
	case domain.IntentTest:
		return []domain.Capability{
			{Name: "verify", Tools: []string{"kern_verify"}, Outputs: []string{"test report"}, Artifacts: []string{"ArtifactTestReport"}, Risk: "low"},
		}
	case domain.IntentDeploy:
		return []domain.Capability{
			{Name: "deploy", Tools: []string{"kern_execute"}, Permissions: []string{"deploy"}, Artifacts: []string{"ArtifactDeploymentReport"}, Risk: "high"},
		}
	case domain.IntentAudit:
		return []domain.Capability{
			{Name: "audit", Tools: []string{"kern_audit"}, Outputs: []string{"audit log"}, Risk: "low"},
		}
	default:
		return []domain.Capability{{Name: "analyze", Tools: []string{"kern_analyze"}, Risk: "low"}}
	}
}

// CapabilitiesToTools flattens a capability list into a tool list.
func CapabilitiesToTools(caps []domain.Capability) []string {
	var tools []string
	seen := map[string]bool{}
	for _, c := range caps {
		for _, t := range c.Tools {
			if !seen[t] {
				tools = append(tools, t)
				seen[t] = true
			}
		}
	}
	return tools
}

// CapabilitiesToAgents returns the specialist agents needed for the capabilities.
func CapabilitiesToAgents(caps []domain.Capability) []string {
	has := map[string]bool{}
	for _, c := range caps {
		for _, n := range c.Dependencies {
			has[n] = true
		}
	}
	var agents []string
	for name := range has {
		agents = append(agents, name)
	}
	if len(agents) == 0 {
		agents = []string{"planner", "coder", "reviewer", "tester"}
	}
	return agents
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// extractTarget heuristically extracts the target symbol/service from the
// intent text (the first CamelCase word or word after "to"/"in").
func extractTarget(raw string) string {
	words := strings.Fields(raw)
	for i, w := range words {
		w = strings.TrimRight(w, ".,;:!?")
		lower := strings.ToLower(w)
		if (lower == "to" || lower == "in" || lower == "the") && i+1 < len(words) {
			next := strings.TrimRight(words[i+1], ".,;:!?")
			if isCamelCase(next) {
				return next
			}
		}
		if isCamelCase(w) {
			return w
		}
	}
	if len(words) > 0 {
		return strings.TrimRight(words[len(words)-1], ".,;:!?")
	}
	return ""
}

func isCamelCase(s string) bool {
	if len(s) < 2 {
		return false
	}
	hasUpper := false
	hasLower := false
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		}
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
	}
	return hasUpper && hasLower
}
