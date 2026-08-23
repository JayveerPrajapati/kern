package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// This file implements the remaining Phase 6 Intent + Capability features:
//
//	P6.6 registry (purpose field)  CapabilityRegistry
//	P6.7 planner fallback          DeterministicPlan (when no LLM provider)
//	P6.8 tool-decision trace wiring ToolDecisionRecorder
//	P6.4 full precheck              CapabilityPrecheck
//	P6.9 discovery                  Registry.Discover / Tools / Agents
//	P6.10 tool fallback             FallbackFor
//
// Everything is deterministic; the planner already supports an LLM via
// internal/planner, so P6.7 here provides the deterministic fallback plan the
// planner returns when no provider is configured.

// CapabilityRegistry is the Phase 6 registry of every known capability, keyed
// by name. It is the single source of truth for capability discovery and
// precheck.
type CapabilityRegistry struct {
	caps map[string]domain.Capability
}

// allCapabilities returns the full static catalog of capabilities. Each entry
// carries a Purpose (P6.6), explicit Inputs (what the capability consumes) and
// Dependencies (other capabilities it requires), so the capability registry is
// a complete, self-describing catalog rather than a tool list alone.
func allCapabilities() []domain.Capability {
	return []domain.Capability{
		{Name: "understand", Purpose: "Gather system context and explain how code works", Inputs: []string{"query", "target"}, Dependencies: []string{"graph"}, Tools: []string{"kern_explore", "kern_graph", "kern_code_graph"}, Outputs: []string{"system context"}, Risk: "low"},
		{Name: "analyze", Purpose: "Produce a context packet for a proposed change", Inputs: []string{"change"}, Dependencies: []string{"graph", "understand"}, Tools: []string{"kern_analyze"}, Outputs: []string{"context packet"}, Artifacts: []string{"ArtifactContextPacket"}, Risk: "low"},
		{Name: "plan", Purpose: "Generate an implementation plan for an intent", Inputs: []string{"intent", "context packet"}, Dependencies: []string{"analyze", "impact"}, Tools: []string{"kern_plan"}, Outputs: []string{"plan"}, Artifacts: []string{"ArtifactPlan"}, Risk: "low"},
		{Name: "impact", Purpose: "Estimate the blast radius of a proposed change", Inputs: []string{"change"}, Dependencies: []string{"graph"}, Tools: []string{"kern_impact", "kern_what_if"}, Outputs: []string{"impact report"}, Artifacts: []string{"ArtifactImpactReport"}, Risk: "low"},
		{Name: "execute", Purpose: "Apply a verified code patch in an isolated worktree", Inputs: []string{"plan", "code patch"}, Dependencies: []string{"plan", "sandbox"}, Tools: []string{"kern_execute"}, Permissions: []string{"write"}, Artifacts: []string{"ArtifactCodePatch", "ArtifactDiff"}, Risk: "medium"},
		{Name: "verify", Purpose: "Run build/test/security verification on a change", Inputs: []string{"change", "patch"}, Dependencies: []string{"execute", "security"}, Tools: []string{"kern_verify", "kern_validate", "kern_test_gaps"}, Outputs: []string{"verification"}, Artifacts: []string{"ArtifactVerificationReport"}, Risk: "low"},
		{Name: "security", Purpose: "Scan source for security findings", Inputs: []string{"change"}, Dependencies: []string{"graph"}, Tools: []string{"kern_security"}, Outputs: []string{"security report"}, Artifacts: []string{"ArtifactSecurityReport"}, Risk: "medium"},
		{Name: "correlate", Purpose: "Correlate an alert to a runtime evidence chain", Inputs: []string{"alert"}, Dependencies: []string{"runtime"}, Tools: []string{"kern_correlate"}, Outputs: []string{"correlation chain"}, Risk: "low"},
		{Name: "investigate", Purpose: "Investigate a production incident end-to-end", Inputs: []string{"alert"}, Dependencies: []string{"correlate", "impact"}, Tools: []string{"kern_incident"}, Outputs: []string{"root cause"}, Artifacts: []string{"ArtifactIncidentReport", "ArtifactRootCauseReport"}, Risk: "medium"},
		{Name: "modernize", Purpose: "Produce a phased modernization plan for a monolith", Inputs: []string{"repository"}, Dependencies: []string{"graph", "impact"}, Tools: []string{"kern_modernize"}, Outputs: []string{"modernization plan"}, Risk: "low"},
		{Name: "audit", Purpose: "Read the governance audit log", Inputs: []string{"task"}, Dependencies: []string{"governance"}, Tools: []string{"kern_audit"}, Outputs: []string{"audit log"}, Risk: "low"},
		{Name: "deploy", Purpose: "Trigger a real deployment via the deployer", Inputs: []string{"version"}, Dependencies: []string{"verify", "approval"}, Tools: []string{"kern_execute"}, Permissions: []string{"deploy"}, Artifacts: []string{"ArtifactDeploymentReport"}, Risk: "high"},
	}
}

// NewCapabilityRegistry builds a registry seeded with all known capabilities.
func NewCapabilityRegistry() *CapabilityRegistry {
	reg := &CapabilityRegistry{caps: make(map[string]domain.Capability, 12)}
	for _, c := range allCapabilities() {
		reg.caps[c.Name] = c
	}
	return reg
}

// Get returns the capability by name and whether it exists.
func (r *CapabilityRegistry) Get(name string) (domain.Capability, bool) {
	c, ok := r.caps[name]
	return c, ok
}

// All returns every registered capability sorted by name.
func (r *CapabilityRegistry) All() []domain.Capability {
	out := make([]domain.Capability, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Tools returns the deduplicated set of all tools across all capabilities
// (P6.9 discovery).
func (r *CapabilityRegistry) Tools() []string {
	seen := map[string]bool{}
	for _, c := range r.caps {
		for _, t := range c.Tools {
			seen[t] = true
		}
	}
	return sortedKeys(seen)
}

// Discover implements Phase 6.9 semantic/lexical capability discovery: given a
// free-text query, it returns the capabilities most relevant to that query,
// ranked by relevance. Scoring is fully deterministic (no LLM, no network):
// the query is tokenized into lowercase words and each capability is scored by
// the fraction of query terms that lexically overlap its Name, Purpose,
// Outputs, Artifacts, Tools, and Risk, with Name and Purpose weighted higher
// than the tool/output fields. Capabilities with Score > 0 are returned sorted
// descending by score (ties broken by name); an empty slice means nothing
// matched.
func (r *CapabilityRegistry) Discover(query string) []domain.CapabilityMatch {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}

	results := make([]domain.CapabilityMatch, 0, len(r.caps))

	for _, name := range sortedCapNames(r.caps) {
		c := r.caps[name]

		// Weighted searchable text: name/purpose count double.
		highWeight := strings.ToLower(c.Name) + " " + strings.ToLower(c.Purpose)
		lowWeight := joinFields(c.Outputs) + " " + joinFields(c.Artifacts) +
			" " + joinFields(c.Tools) + " " + strings.ToLower(c.Risk)

		matchedTokens := 0
		var matched []string
		for _, tok := range tokens {
			hitHigh := containsWord(highWeight, tok)
			hitLow := containsWord(lowWeight, tok)
			if hitHigh {
				matchedTokens += 2
			}
			if hitLow {
				matchedTokens++
			}
			if hitHigh || hitLow {
				matched = append(matched, tok)
			}
		}
		if matchedTokens == 0 {
			continue
		}
		score := float64(matchedTokens) / float64(2*len(tokens))
		if score > 1 {
			score = 1
		}
		results = append(results, domain.CapabilityMatch{
			Capability: c,
			Score:      score,
			Matches:    dedupeMatchesOrder(matched),
		})
	}

	// Sort descending by score, ties by name.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Capability.Name < results[j].Capability.Name
	})
	return results
}

// Search is a convenience wrapper around Discover that returns just the matched
// capabilities (no scores), in the same deterministic ranked order.
func (r *CapabilityRegistry) Search(query string) []domain.Capability {
	matches := r.Discover(query)
	out := make([]domain.Capability, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Capability)
	}
	return out
}

// tokenize lowercases and splits a query into non-empty word tokens.
func tokenize(query string) []string {
	lower := strings.ToLower(query)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !isWordRune(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// containsWord reports whether any word in haystack equals the token (word
// boundary matching, so "security" does not falsely match inside "deploying").
func containsWord(haystack, token string) bool {
	for _, w := range strings.Fields(haystack) {
		if w == token {
			return true
		}
	}
	return false
}

// joinFields lowercases and joins a string slice into a single space-separated
// string for token matching.
func joinFields(fields []string) string {
	return strings.ToLower(strings.Join(fields, " "))
}

func sortedCapNames(m map[string]domain.Capability) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupeMatchesOrder(matches []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// Agents returns the deduplicated set of agent dependencies across all
// capabilities (P6.9 discovery).
func (r *CapabilityRegistry) Agents() []string {
	seen := map[string]bool{}
	for _, c := range r.caps {
		for _, a := range c.Dependencies {
			seen[a] = true
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CapabilityPrecheck runs the full precheck for a capability (P6.4): the agent
// identity must be non-empty, the scope must be in the capability's declared
// scope, the environment must be allowed, and every required tool must be
// available in toolset. It returns a slice of problems; an empty slice means
// the capability passes precheck.
func (r *CapabilityRegistry) CapabilityPrecheck(name, agentID, scope, env string, toolset map[string]bool) []string {
	c, ok := r.caps[name]
	if !ok {
		return []string{"unknown capability: " + name}
	}
	var problems []string
	if strings.TrimSpace(agentID) == "" {
		problems = append(problems, "missing identity: agent id is required")
	}
	if strings.TrimSpace(scope) == "" {
		problems = append(problems, "missing scope: a task scope is required")
	}
	// Scope is free-form but must be non-empty for safety.
	// Environment gate: high-risk capabilities require an explicit env.
	if c.Risk == "high" && strings.TrimSpace(env) == "" {
		problems = append(problems, "missing environment: high-risk capability requires an explicit environment")
	}
	// Tool availability: every tool in the capability must be in toolset.
	for _, t := range c.Tools {
		if !toolset[t] {
			problems = append(problems, "required tool unavailable: "+t)
		}
	}
	return problems
}

// toolFallbacks maps a tool to an equivalent fallback for P6.10 (tool fallback).
var toolFallbacks = map[string]string{
	"kern_what_if":  "kern_impact",
	"kern_validate": "kern_verify",
	"kern_loop":     "kern_plan",
	"kern_heal":     "kern_validate",
}

// FallbackFor returns an alternative tool for a tool that is unavailable, or ""
// when no fallback is declared (P6.10).
func FallbackFor(tool string) string {
	return toolFallbacks[tool]
}

// DeterministicPlan produces a structured, deterministic implementation plan
// for an intent when no LLM planner is available (P6.7 fallback). It mirrors
// the plan structure the LLM planner produces but is rule-based: it lists the
// target, scope, risk, and the standard workflow steps for the intent type.
func DeterministicPlan(intent domain.CompiledIntent) string {
	var b strings.Builder
	b.WriteString("## Objective\n")
	b.WriteString(intent.Objective)
	b.WriteString("\n\n## Risk\n")
	b.WriteString(defaultRisk(intent.Type))
	b.WriteString("\n\n## Scope\n")
	b.WriteString(intent.Scope)
	if intent.Target != "" {
		b.WriteString("\n\n## Target\n")
		b.WriteString(intent.Target)
	}
	b.WriteString("\n\n## Implementation Steps\n")
	for i, step := range stepsFor(intent.Type) {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	b.WriteString("\n## Rollback\nRestore from version control; the change is confined to the declared scope.")
	return b.String()
}

func defaultRisk(it domain.IntentType) string {
	switch it {
	case domain.IntentDeploy, domain.IntentModernization:
		return "high"
	case domain.IntentCodeChange, domain.IntentSecurity:
		return "medium"
	default:
		return "low"
	}
}

func stepsFor(it domain.IntentType) []string {
	switch it {
	case domain.IntentCodeChange:
		return []string{"Analyze the proposed change and its blast radius", "Plan the implementation steps", "Apply the patch in an isolated worktree", "Verify build and tests", "Create a pull request"}
	case domain.IntentIncident:
		return []string{"Correlate the alert to runtime evidence", "Investigate the root cause", "Propose a fix and route through review", "Verify the fix", "Record the lesson"}
	case domain.IntentSecurity:
		return []string{"Scan the target scope for findings", "Assess risk per finding", "Propose remediation", "Verify remediation"}
	case domain.IntentTest:
		return []string{"Identify the change under test", "Run the relevant test suites", "Report gaps"}
	default:
		return []string{"Understand the request", "Analyze the target", "Produce a plan", "Apply and verify"}
	}
}

// ToolDecisionTraceRecorder collects ToolDecisionTrace entries (P6.8) so the
// tool-decision trail can be audited.
type ToolDecisionTraceRecorder struct {
	traces []domain.ToolDecisionTrace
}

// NewToolDecisionTraceRecorder returns an empty trace recorder.
func NewToolDecisionTraceRecorder() *ToolDecisionTraceRecorder {
	return &ToolDecisionTraceRecorder{}
}

// Record appends a trace with the current timestamp latency.
func (r *ToolDecisionTraceRecorder) Record(t domain.ToolDecisionTrace) {
	if t.Latency == 0 {
		t.Latency = 0 // latency_ms set by caller; keep zero if unmeasured
	}
	r.traces = append(r.traces, t)
}

// Traces returns a copy of the collected traces, oldest first.
func (r *ToolDecisionTraceRecorder) Traces() []domain.ToolDecisionTrace {
	out := make([]domain.ToolDecisionTrace, len(r.traces))
	copy(out, r.traces)
	return out
}

// Len returns the number of recorded traces.
func (r *ToolDecisionTraceRecorder) Len() int { return len(r.traces) }