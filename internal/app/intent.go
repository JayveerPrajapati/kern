package app

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// CompileIntent classifies a raw intent string into a CompiledIntent with an
// IntentType, objective, target, scope, and environment. .
// Classification is deterministic (keyword/verb matching, no LLM):
// - "explain"/"understand"/"what does" → UNDERSTAND
// - "add"/"implement"/"fix"/"refactor"/"remove" → CODE_CHANGE
// - "review"/"check" → REVIEW
// - "what if"/"simulate"/"predict" → WHAT_IF
// - "incident"/"alert"/"failing"/"down" → INCIDENT
// - "modernize"/"split"/"extract" → MODERNIZATION
// - "security"/"vulnerab"/"secret" → SECURITY
// - "test" → TEST
// - "deploy"/"release" → DEPLOY
// - "audit"/"who changed"/"what did" → AUDIT
// The compiled environment is derived from the intent type: production
// operations (DEPLOY, INCIDENT) compile to "production"; everything else
// defaults to "development". Scope is "repository".
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
	case containsAny(lower, "incident", "alert", "failing", "down", "outage"):
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
		Environment:    environmentFor(it),
		DesiredOutcome: raw,
		RawText:        raw,
	}
}

// environmentFor derives the operating environment from the intent type.
// Production operations (deploy, incident response) target "production"; all
// other intents default to "development". The compiler derives this so the
// precheck's environment gate can enforce the correct boundary for a request —
// a deploy must be scoped to production, not silently reported as development.
func environmentFor(it domain.IntentType) string {
	switch it {
	case domain.IntentDeploy, domain.IntentIncident:
		return "production"
	default:
		return "development"
	}
}

// SelectWorkflow maps an IntentType to a primary workflow (A-E).
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
// The capability planner selects only the required
// capabilities. It resolves names through the canonical CapabilityRegistry
// (allCapabilities) so the Run path uses the SAME single source of truth as
// discovery/precheck — there is exactly one capability catalog, and every
// capability returned carries its Purpose, Inputs and Dependencies.
func DefaultCapabilities(it domain.IntentType) []domain.Capability {
	reg := NewCapabilityRegistry()
	names := capabilityNamesFor(it)
	caps := make([]domain.Capability, 0, len(names))
	for _, n := range names {
		if c, ok := reg.Get(n); ok {
			caps = append(caps, c)
		}
	}
	return caps
}

// capabilityNamesFor returns the canonical capability names an intent type
// requires. This is the only place intent → capability membership is declared;
// the actual capability metadata (tools, permissions, risk, purpose) lives in
// allCapabilities().
func capabilityNamesFor(it domain.IntentType) []string {
	switch it {
	case domain.IntentUnderstand:
		return []string{"understand"}
	case domain.IntentCodeChange:
		return []string{"analyze", "plan", "impact", "execute", "verify", "pr"}
	case domain.IntentWhatIf:
		return []string{"whatif"}
	case domain.IntentIncident:
		return []string{"correlate", "investigate"}
	case domain.IntentModernization:
		return []string{"modernize"}
	case domain.IntentSecurity:
		return []string{"security"}
	case domain.IntentTest:
		return []string{"verify"}
	case domain.IntentDeploy:
		return []string{"deploy"}
	case domain.IntentAudit:
		return []string{"audit"}
	default:
		return []string{"analyze"}
	}
}

// contextPlanFor derives the RunResult context plan for an intent from the
// capabilities it requires, in execution order. It is deterministic and
// intent-aware: the plan is a faithful projection of the canonical capability
// membership rather than a hardcoded universal string, so a WHAT_IF run shows
// "whatif", an INCIDENT run shows its correlate/investigate sequence, and so
// on. For CODE_CHANGE the canonical execution order is preserved (analyze →
// context → memory → impact → risk → plan → execute → verify → pr).
func contextPlanFor(it domain.IntentType) string {
	switch it {
	case domain.IntentCodeChange:
		return "analyze → context → memory → impact → risk → plan → execute → verify → pr"
	default:
		return strings.Join(capabilityNamesFor(it), " → ")
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

// capabilityAgents maps each capability name to the specialist roles that
// execute it. This is the canonical capability → role bridge: capabilities are
// NOT agents, so a capability's Dependencies field (other capabilities) must
// never be mistaken for the agent team. The mapping produces a deterministic,
// intent-aware agent team instead of a flat default.
var capabilityAgents = map[string][]string{
	"understand":  {"architect"},
	"analyze":     {"architect"},
	"impact":      {"architect"},
	"whatif":      {"architect"},
	"plan":        {"planner"},
	"modernize":   {"architect", "planner"},
	"execute":     {"coder"},
	"pr":          {"coder"},
	"verify":      {"tester"},
	"security":    {"security"},
	"correlate":   {"sre"},
	"investigate": {"sre"},
	"deploy":      {"sre", "planner"},
	"audit":       {"reviewer"},
}

// fixedAgentOrder is the canonical specialist-role ordering so the returned
// agent team is stable and deterministic.
var fixedAgentOrder = []string{"architect", "planner", "coder", "reviewer", "tester", "security", "sre"}

// CapabilitiesToAgents returns the specialist agent team required by the
// capabilities, derived from the canonical capability → role mapping in fixed
// role order. It never treats a capability dependency as an agent.
func CapabilitiesToAgents(caps []domain.Capability) []string {
	want := map[string]bool{}
	for _, c := range caps {
		for _, role := range capabilityAgents[c.Name] {
			want[role] = true
		}
	}
	var agents []string
	for _, role := range fixedAgentOrder {
		if want[role] {
			agents = append(agents, role)
		}
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
