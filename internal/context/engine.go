// Package context implements the Context Engine 2.0: it assembles a
// domain.ContextPacket combining the intelligence graph, engineering memory,
// evidence, architecture, git, and risk into one structured response for the
// "Analyze this proposed change" workflow.
package context

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// engineAgent is the default agent identity the engine uses when assessing
// risk through the governance firewall.
const engineAgent = "context-engine"

// Engine assembles a ContextPacket from the intelligence graph, memory store,
// evidence factory, and governance firewall. It is the core of Context
// Engine 2.0.
type Engine struct {
	graph    *intelligence.Graph
	memory   *memory.MemoryStore
	firewall *governance.Firewall
	root     string // project root (for git operations)

	// runtimeSrc is an optional production-intelligence source. When nil, the
	// engine emits no runtime evidence (empty slice) and behaves as before.
	runtimeSrc runtime.Source
	// boundaryProvider optionally supplies architecture boundary rules (as
	// domain.Policy, e.g. via domain.FromGuardRule) for the engine to surface
	// when a change crosses a forbidden boundary. Nil = none.
	boundaryProvider func() []domain.Policy
	// bus is an optional event publisher (context_packet.built). nil = no-op.
	bus *eventbus.Bus

	// maxTokens is an optional token budget for the rendered packet. When 0
	// (default), no budgeting is applied (backward-compatible). When >0, the
	// packet's rendered text is fitted to this budget when it overflows.
	maxTokens int

	// freshnessScoring gates the freshness adjustments (risk-score
	// scaling and evidence-confidence scaling). It is OFF by default so the
	// engine's prior output is unchanged; callers opt in with
	// WithFreshnessScoring(true) to enable it.
	freshnessScoring bool

	// nodesByIDCache caches the node ID -> node lookup, built once on first use.
	// The graph is fixed at Engine construction, so the cache never goes stale.
	nodesByIDOnce  sync.Once
	nodesByIDCache map[string]domain.Node
}

// NewEngine creates a context engine with the given dependencies.
func NewEngine(root string, graph *intelligence.Graph, mem *memory.MemoryStore, fw *governance.Firewall) *Engine {
	return &Engine{graph: graph, memory: mem, firewall: fw, root: root}
}

// WithRuntimeSource attaches an optional production runtime source. When nil,
// RuntimeEvidence stays an empty slice (never nil) and prior behavior is
// preserved.
func (e *Engine) WithRuntimeSource(src runtime.Source) *Engine {
	e.runtimeSrc = src
	return e
}

// WithBoundaryProvider attaches an optional provider of architecture boundary
// rules (as domain.Policy). When set, the engine surfaces a boundary rule in
// ArchitectureRules only when the change actually crosses that boundary. A nil
// provider keeps governance policies as the source of architecture rules.
func (e *Engine) WithBoundaryProvider(provider func() []domain.Policy) *Engine {
	e.boundaryProvider = provider
	return e
}

// WithBus attaches an optional event bus. When non-nil, the engine publishes
// context_packet.built for each assembled packet. A nil bus is a no-op.
func (e *Engine) WithBus(b *eventbus.Bus) *Engine {
	e.bus = b
	return e
}

// WithMaxTokens sets an optional token budget for the rendered packet. When
// maxTokens > 0 and the packet exceeds it, the rendered text is budget-fitted
// (see FittedText). A value of 0 (the default) applies no budgeting. It returns
// e for chaining.
func (e *Engine) WithMaxTokens(n int) *Engine {
	e.maxTokens = n
	return e
}

// WithFreshnessScoring enables (or disables) the freshness scoring
// adjustments in the assembled packet: risk scores are scaled by the freshness
// of the runtime evidence, and claim confidence is scaled by the freshness of
// its evidence. It is OFF by default to keep the engine's output identical to
// prior behavior; it returns e for chaining.
func (e *Engine) WithFreshnessScoring(enabled bool) *Engine {
	e.freshnessScoring = enabled
	return e
}

// AnalyzeChange produces a ContextPacket for a proposed change to the given
// symbol. Pipeline: resolve the symbol, then gather relevant code, architecture,
// dependencies, memory, blast radius, risk, evidence-backed claims, required
// validation, and a token count.
func (e *Engine) AnalyzeChange(symbol string) (domain.ContextPacket, error) {
	start := time.Now()
	target, ok := e.resolveNode(symbol)
	if !ok || target.Symbol == nil {
		return domain.ContextPacket{}, fmt.Errorf("context: symbol %q not found in graph", symbol)
	}
	pkt := e.assemble("Analyze this proposed change: "+symbol, symbol, []domain.Symbol{*target.Symbol})
	metrics.Default().RecordContextRetrieval(time.Since(start))
	metrics.Default().RecordAnalysis()
	return pkt, nil
}

// AnalyzeFile produces a ContextPacket for a proposed change to a file (not a
// specific symbol). Every symbol defined in the file becomes a root, and the
// file path is used as the recall scope.
func (e *Engine) AnalyzeFile(filePath string) (domain.ContextPacket, error) {
	var roots []domain.Symbol
	for _, n := range e.graph.Nodes {
		if n.Symbol != nil && n.Symbol.File == filePath {
			roots = append(roots, *n.Symbol)
		}
	}
	if len(roots) == 0 {
		return domain.ContextPacket{}, fmt.Errorf("context: no symbols found in file %q", filePath)
	}
	return e.assemble("Analyze this proposed change to file: "+filePath, filePath, roots), nil
}

// analyzeIntent parses a change description into a structured Intent using
// deterministic keyword matching (no LLM).
func analyzeIntent(text string) domain.Intent {
	i := domain.Intent{RawText: text}
	lower := strings.ToLower(text)

	// Detect verbs.
	verbSet := map[string]bool{}
	for _, v := range []string{"add", "remove", "delete", "refactor", "fix", "update", "test", "implement", "migrate", "optimize", "rename"} {
		if strings.Contains(lower, v) {
			verbSet[v] = true
		}
	}
	for v := range verbSet {
		i.Verbs = append(i.Verbs, v)
	}

	// Detect categories from verbs and content.
	catSet := map[string]bool{}
	if verbSet["add"] || verbSet["implement"] {
		catSet["feature"] = true
	}
	if verbSet["fix"] {
		catSet["bugfix"] = true
	}
	if verbSet["refactor"] || verbSet["rename"] || verbSet["migrate"] {
		catSet["refactor"] = true
	}
	if verbSet["test"] {
		catSet["test"] = true
	}
	if strings.Contains(lower, "doc") || strings.Contains(lower, "readme") {
		catSet["docs"] = true
	}
	if strings.Contains(lower, "config") || strings.Contains(lower, "yaml") || strings.Contains(lower, "json") {
		catSet["config"] = true
	}
	for c := range catSet {
		i.Categories = append(i.Categories, c)
	}

	// Targets: extract quoted strings, file paths, and CamelCase identifiers
	// (best-effort deterministic extraction, no LLM).
	i.Targets = extractTargets(text)

	return i
}

// extractTargets pulls candidate target nouns from a change description:
// quoted strings, file paths (containing a path separator), and identifiers
// that look like symbols (contain an uppercase letter). Results are
// deduplicated case-insensitively in first-seen order.
func extractTargets(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(w string) {
		w = strings.Trim(w, `"'.,;:()[]{}`)
		w = strings.TrimSpace(w)
		key := strings.ToLower(w)
		if w == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, w)
	}

	// Quoted strings and paths.
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '\t'
	}) {
		if strings.Contains(f, `"`) || strings.Contains(f, `'`) || strings.Contains(f, "/") {
			add(f)
		}
	}

	// CamelCase / PascalCase identifiers (contain an uppercase letter).
	for _, f := range strings.Fields(text) {
		hasUpper := false
		hasLetter := false
		for _, r := range f {
			if r >= 'A' && r <= 'Z' {
				hasUpper = true
			}
			if r >= 'a' && r <= 'z' {
				hasLetter = true
			}
		}
		if hasUpper && hasLetter {
			add(f)
		}
	}

	return out
}

// assemble builds the ContextPacket. It is the shared core for both
// AnalyzeChange and AnalyzeFile. scope is the analysis scope (symbol or file);
// roots are the primary symbols under change.
func (e *Engine) assemble(task, scope string, roots []domain.Symbol) domain.ContextPacket {
	byID := e.nodesByID()

	pkt := domain.ContextPacket{
		Task:        task,
		GeneratedAt: time.Now(),
	}

	// Interpret the change request into a deterministic Intent (no LLM).
	pkt.Intent = analyzeIntent(task)

	pkt.Symbols = e.symbolSet(byID, roots)
	pkt.Files = filesOf(pkt.Symbols)
	pkt.Dependencies = e.dependencyEdges(scope, roots)

	// ArchitectureRules depends on the governance firewall and optional boundary
	// provider (data dependency: when neither is wired, or no rule applies, the
	// rule set is empty). Normalize to an empty slice so it is NEVER nil —
	// consumers can rely on len() == 0 rather than a nil check.
	pkt.ArchitectureRules = e.architectureRules(scope, roots)
	if pkt.ArchitectureRules == nil {
		pkt.ArchitectureRules = []domain.Policy{}
	}

	if e.memory != nil {
		if mem, err := e.memory.Recall(memory.Query{Scope: scope, Limit: 10}); err == nil {
			pkt.Memory = mem
		}
		if inc, err := e.memory.Recall(memory.Query{Type: domain.MemoryIncident, Scope: scope, Limit: 5}); err == nil {
			pkt.Incidents = inc
		}
	}

	// RuntimeEvidence depends on an optional production runtime source. When no
	// source is wired (or nothing matches), it is an empty slice (never nil) so
	// consumers can rely on len() == 0 rather than a nil check.
	pkt.RuntimeEvidence = e.runtimeEvidence(roots)
	if pkt.RuntimeEvidence == nil {
		pkt.RuntimeEvidence = []domain.Evidence{}
	}

	// Assess through the firewall using the real change scope so sensitive
	// changes are scored higher and denials surface as Blocked/ApprovalRequired.
	pkt.Risks = e.assessRisk(scope, roots)

	pkt.Facts = e.buildFacts(scope, roots, pkt)
	// Surface one evidence-backed claim per risk so the governance decision
	// flows into the packet.
	pkt.Facts = append(pkt.Facts, policyEvaluationClaims(pkt.Risks)...)

	// Freshness scoring (opt-in): when enabled, scale each risk's
	// Score by the freshness of the packet's runtime evidence (score-only;
	// Level and Blocked/ApprovalRequired are preserved), and scale each claim's
	// Confidence by the freshness of its supporting evidence. This is gated by
	// freshnessScoring (default OFF) so prior output is unchanged.
	if e.freshnessScoring {
		now := time.Now()
		if len(pkt.RuntimeEvidence) > 0 {
			for i := range pkt.Risks {
				pkt.Risks[i] = FreshnessAdjustedRisk(pkt.Risks[i], pkt.RuntimeEvidence, now, 0)
			}
		}
		for i := range pkt.Facts {
			ev := pkt.Facts[i].Evidence
			if len(ev) == 0 {
				// Approximate: claims without their own evidence are scored
				// against the packet's runtime evidence as the freshness signal.
				ev = pkt.RuntimeEvidence
			}
			if pkt.Facts[i].Confidence > 0 {
				pkt.Facts[i].Confidence = FreshnessAdjustedConfidence(pkt.Facts[i].Confidence, ev, now, 0)
			}
		}
	}

	// Exit gate: cross-engine consistency. Conflicting claims (graph
	// vs memory vs runtime vs git ...) about the same subject are NEVER
	// silently collapsed into certainty — the conflicting claims' confidence
	// is downgraded and the report (result, per-conflict explanation,
	// staleness attribution) is attached to the packet.
	ApplyConsistency(&pkt)

	pkt.RequiredValidation = e.requiredValidation(scope, roots)
	pkt.TokenCount = e.measureTokens(pkt)
	tokenBefore := pkt.TokenCount

	// When MaxTokens is set and the packet exceeds it, fit the rendered text to
	// the budget; structured fields are preserved unchanged for struct consumers.
	if e.maxTokens > 0 && pkt.TokenCount > e.maxTokens {
		fitted := budget.Fit(RenderText(pkt), e.maxTokens)
		pkt.TokenCount = tokenize.Count(fitted)
		pkt.FittedText = fitted
	}

	// Record token usage (before → after budget fit; equal when no budget).
	metrics.Default().RecordTokenUsage(int64(tokenBefore), int64(pkt.TokenCount))

	if e.bus != nil {
		e.bus.Publish(eventbus.Event{
			Kind:    eventbus.ContextPacketBuilt,
			Source:  "context",
			Subject: scope,
			Payload: map[string]string{"task": task, "symbols": fmt.Sprintf("%d", len(pkt.Symbols)), "files": fmt.Sprintf("%d", len(pkt.Files))},
		})
	}

	return pkt
}

// symbolSet collects the roots plus their direct callers and direct callees
// (depth 1) into a deduplicated, deterministically-ordered symbol list.
func (e *Engine) symbolSet(byID map[string]domain.Node, roots []domain.Symbol) []domain.Symbol {
	seen := map[string]bool{}
	var out []domain.Symbol
	add := func(s domain.Symbol) {
		if s.Qualified == "" || seen[s.Qualified] {
			return
		}
		seen[s.Qualified] = true
		out = append(out, s)
	}
	for _, r := range roots {
		add(r)
		// Direct callees (depth 1): edges out of the root symbol.
		for _, to := range e.directCallees(r.Qualified) {
			if n, ok := byID[to]; ok && n.Symbol != nil {
				add(*n.Symbol)
			}
		}
		// Direct callers.
		for _, c := range e.graph.WhoCalls(r.Qualified) {
			if c.Symbol != nil {
				add(*c.Symbol)
			}
		}
	}
	return out
}

// directCallees returns the node IDs directly called by symbol (depth 1).
func (e *Engine) directCallees(symbol string) []string {
	var out []string
	for _, edge := range e.graph.Edges {
		if edge.Kind == "calls" && edge.From == symbol {
			out = append(out, edge.To)
		}
	}
	return out
}

// dependencyEdges returns the call edges whose endpoints lie within the blast
// region reachable from the roots (WhatDependsOn ∪ WhatDoesXDependOn).
func (e *Engine) dependencyEdges(scope string, roots []domain.Symbol) []domain.Edge {
	region := map[string]bool{}
	var rootIDs []string
	for _, r := range roots {
		rootIDs = append(rootIDs, r.Qualified)
		region[r.Qualified] = true
	}
	for _, id := range rootIDs {
		for _, n := range e.graph.WhatDependsOn(id) {
			region[n.ID] = true
		}
		for _, n := range e.graph.WhatDoesXDependOn(id) {
			region[n.ID] = true
		}
	}
	seen := map[string]bool{}
	var out []domain.Edge
	for _, edge := range e.graph.Edges {
		if !region[edge.From] || !region[edge.To] {
			continue
		}
		key := edge.From + "|" + edge.To + "|" + edge.Kind
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, edge)
	}
	return out
}

// assessRisk runs the governance firewall check for the change scope (the
// roots' affected files). It captures allowed + risk level + approval requirement,
// sets Blocked on denial, and ApprovalRequired on a pending approval gate.
// The mapping is deterministic: the first security-sensitive root file wins.
// Without a firewall it returns a documented MEDIUM default.
func (e *Engine) assessRisk(scope string, roots []domain.Symbol) []domain.Risk {
	if e.firewall == nil {
		// Documented default: without a firewall we cannot authoritatively
		// classify the change, so surface a MEDIUM source write by default.
		return []domain.Risk{{
			Level:      domain.RiskMedium,
			Score:      0.5,
			Factors:    []string{"no governance firewall configured; defaulting to MEDIUM source.write"},
			Mitigation: "wire a governance firewall to obtain precise risk classification",
		}}
	}

	resource, action := resourceForAction(roots)
	allowed, risk, approval, err := e.firewall.Check(engineAgent, resource, action)

	// The firewall may return a zero-valued risk on early denials; normalize.
	if risk.Level == "" {
		risk.Level = domain.RiskLow
	}
	if risk.Score == 0 {
		risk.Score = riskScore(risk.Level)
	}
	if risk.Factors == nil {
		risk.Factors = []string{}
	}
	risk.Factors = append(risk.Factors, fmt.Sprintf("governance:%s:%s", resource, action))

	// A firewall error means the action was denied (fail-closed).
	if err != nil {
		risk.Blocked = true
		if risk.Mitigation == "" {
			risk.Mitigation = err.Error()
		}
	}
	// A pending approval requires human sign-off before the change proceeds.
	if approval != nil {
		risk.ApprovalRequired = true
	}
	// Denials (missing permission / always-blocked) also block the change.
	if !allowed {
		risk.Blocked = true
	}

	// Adjust risk based on blast radius: a tiny isolated change should not be
	// CRITICAL just because it touches source code. The firewall's
	// resource-based level is the ceiling; the actual risk is proportional to
	// how much of the system is affected. Security-sensitive changes are the
	// exception — their HIGH escalation is inherent to the resource (auth,
	// credentials, TLS, ...) and is intentionally NOT downgraded by scope size.
	blastRadius := len(roots)
	isSecuritySensitive := resource == "security"
	switch {
	case blastRadius <= 1 && !isSecuritySensitive:
		// Isolated change — cap at MEDIUM regardless of firewall level.
		if riskScore(risk.Level) > riskScore(domain.RiskMedium) {
			risk.Level = domain.RiskMedium
			risk.Score = riskScore(domain.RiskMedium)
		}
		risk.Factors = append(risk.Factors, "blast-radius:isolated")
	case blastRadius <= 5 && !isSecuritySensitive:
		// Moderate change — cap at HIGH.
		if riskScore(risk.Level) > riskScore(domain.RiskHigh) {
			risk.Level = domain.RiskHigh
			risk.Score = riskScore(domain.RiskHigh)
		}
		risk.Factors = append(risk.Factors, "blast-radius:moderate")
	default:
		risk.Factors = append(risk.Factors, "blast-radius:large")
	}

	return []domain.Risk{risk}
}

// policyEvaluationClaims converts each assessed Risk into an evidence-backed
// FromPolicyEvaluation FACT claim so the governance decision is surfaced in the
// packet's facts. It derives the policy rule from the risk's governance factor
// (e.g. "governance:source:write") when present, falling back to a stable
// label.
func policyEvaluationClaims(risks []domain.Risk) []domain.Claim {
	var out []domain.Claim
	for _, r := range risks {
		rule := "context:risk"
		for _, f := range r.Factors {
			if strings.HasPrefix(f, "governance:") {
				rule = f
				break
			}
		}
		reason := r.Mitigation
		if reason == "" {
			reason = string(r.Level)
		}
		out = append(out, evidence.FromPolicyEvaluation(rule, !r.Blocked, reason))
	}
	return out
}

// resourceForAction maps the roots under change to a governance resource +
// action based on the affected files. It is deterministic: roots are inspected
// in order and the first match wins, with security-sensitive files taking
// priority so they are never downgraded to a benign source change.
func resourceForAction(roots []domain.Symbol) (resource, action string) {
	const writeAction = "write"
	for _, r := range roots {
		f := strings.ToLower(r.File)
		switch {
		case isSecurityFile(f):
			return "security", writeAction
		case isTestFile(f):
			return "tests", writeAction
		case isConfigFile(f):
			return "config", writeAction
		case isDocFile(f):
			return "documentation", writeAction
		}
	}
	return "source", writeAction
}

// isSecurityFile reports whether a root-relative file path is security
// sensitive and must be classified under the security policy (HIGH, approval
// required) rather than an ordinary source change.
func isSecurityFile(f string) bool {
	for _, k := range []string{
		"auth", "credential", "secret", "token", "password", "security",
		"tls", "pem", "crt", "key", "oauth", "session", "vault",
	} {
		if strings.Contains(f, k) {
			return true
		}
	}
	return false
}

// isTestFile reports whether a root-relative path denotes a test file.
func isTestFile(f string) bool {
	return strings.Contains(f, "_test.") || strings.Contains(f, ".test.")
}

// isConfigFile reports whether a root-relative path denotes a configuration
// file.
func isConfigFile(f string) bool {
	for _, ext := range []string{".yaml", ".yml", ".json", ".toml", ".ini", ".env", ".conf", "config", "settings"} {
		if strings.Contains(f, ext) {
			return true
		}
	}
	return false
}

// isDocFile reports whether a root-relative path denotes documentation.
func isDocFile(f string) bool {
	return strings.Contains(f, "docs") ||
		strings.HasSuffix(f, ".md") ||
		strings.HasSuffix(f, ".adoc") ||
		strings.HasSuffix(f, ".txt") ||
		strings.HasPrefix(f, "readme")
}

// riskScore returns the deterministic 0.0-1.0 score for a risk level. It is
// kept local so the context engine does not depend on governance internals.
func riskScore(level domain.RiskLevel) float64 {
	switch level {
	case domain.RiskCritical:
		return 1.0
	case domain.RiskHigh:
		return 0.75
	case domain.RiskMedium:
		return 0.5
	default:
		return 0.25
	}
}

// buildFacts assembles evidence-backed claims for the packet: the graph blast
// radius (INFERENCE), a git diff fact (if available), recalled memory
// (INFERENCE), and test-coverage facts.
func (e *Engine) buildFacts(scope string, roots []domain.Symbol, pkt domain.ContextPacket) []domain.Claim {
	var facts []domain.Claim

	// 1. Graph impact: blast radius for each root.
	for _, r := range roots {
		affected := nodeIDs(e.graph.WhatDependsOn(r.Qualified))
		if len(affected) == 0 {
			affected = []string{r.Qualified}
		}
		facts = append(facts, evidence.FromGraphImpact(r.Qualified, affected))
	}

	// 2. Git diff fact: only when a working diff is available for a root's file.
	for _, r := range roots {
		if r.File == "" {
			continue
		}
		if diff := e.gitDiff(r.File); diff != "" {
			facts = append(facts, evidence.FromGitChange(r.File, diff))
		}
	}

	// 3. Test-coverage facts: tests that exercise the affected symbols.
	for _, r := range roots {
		for _, t := range e.graph.WhatTestsCover(r.Qualified) {
			if t.Symbol == nil {
				continue
			}
			facts = append(facts, testCoverageClaim(t.Symbol.Name, r.Qualified))
		}
	}

	// 4. Recalled memory + incidents: INFERENCE claims.
	for _, m := range pkt.Memory {
		facts = append(facts, evidence.FromMemoryRecall(m))
	}
	for _, m := range pkt.Incidents {
		facts = append(facts, evidence.FromMemoryRecall(m))
	}

	// 5. Recommendation: the concrete validation actions the change requires.
	for _, r := range roots {
		steps := e.requiredValidation(scope, []domain.Symbol{r})
		if len(steps) == 0 {
			continue
		}
		join := strings.Join(steps, "; ")
		facts = append(facts, evidence.NewBuilder(domain.ClaimRecommendation,
			"Validate change to "+r.Qualified+": "+join).
			WithSource("context").
			WithProvenance("context:recommendation").
			WithScope(r.Qualified).
			WithConfidence(evidence.ConfidenceHigh).
			WithEvidence(domain.Evidence{
				Type:      domain.EvidenceGraph,
				Source:    "context",
				Content:   join,
				Digest:    digestOf(join),
				Timestamp: time.Now(),
			}).
			Build())
	}

	return facts
}

// testCoverageClaim builds a FACT claim that a test covers the given symbol.
func testCoverageClaim(testName, target string) domain.Claim {
	return evidence.NewBuilder(domain.ClaimFact,
		fmt.Sprintf("Test %s covers %s", testName, target)).
		WithSource("intel").
		WithProvenance("intel:graph").
		WithScope(target).
		WithConfidence(evidence.ConfidenceCertain).
		WithEvidence(domain.Evidence{
			Type:    domain.EvidenceTest,
			Source:  "intel",
			Content: testName + " covers " + target,
			Digest:  digestOf(testName + target),
		}).
		Build()
}

// requiredValidation derives the verification steps needed for the change.
func (e *Engine) requiredValidation(scope string, roots []domain.Symbol) []string {
	var steps []string
	for _, r := range roots {
		tests := nodeIDs(e.graph.WhatTestsCover(r.Qualified))
		if len(tests) > 0 {
			steps = append(steps, fmt.Sprintf("run unit tests covering %s (%s)", r.Qualified, strings.Join(tests, ", ")))
		} else {
			steps = append(steps, fmt.Sprintf("write and run unit tests for %s", r.Qualified))
		}
	}
	// Build verification.
	steps = append(steps, "build verification")
	// Integration tests for affected services.
	for _, r := range roots {
		for _, svc := range e.graph.WhatServicesAffected(r.Qualified) {
			if svc.Label != "" {
				steps = append(steps, "integration tests for affected service "+svc.Label)
			}
		}
	}
	return steps
}

// measureTokens estimates the packet's token count. To keep the measurement
// stable for the same input, the JSON is computed over a copy with all
// time-based fields zeroed (GeneratedAt and per-claim/per-memory timestamps),
// so wall-clock differences never leak into the token count.
func (e *Engine) measureTokens(pkt domain.ContextPacket) int {
	stable := pkt
	stable.GeneratedAt = time.Time{}

	// Deep-copy the slices so zeroing times here never mutates pkt itself
	// (slices share backing arrays).
	stable.Facts = make([]domain.Claim, len(pkt.Facts))
	for i := range pkt.Facts {
		stable.Facts[i] = pkt.Facts[i]
		stable.Facts[i].Timestamp = time.Time{}
		stable.Facts[i].Evidence = make([]domain.Evidence, len(pkt.Facts[i].Evidence))
		copy(stable.Facts[i].Evidence, pkt.Facts[i].Evidence)
		for j := range stable.Facts[i].Evidence {
			stable.Facts[i].Evidence[j].Timestamp = time.Time{}
		}
	}
	stable.Memory = append([]domain.Memory(nil), pkt.Memory...)
	for i := range stable.Memory {
		stable.Memory[i].CreatedAt = time.Time{}
		stable.Memory[i].UpdatedAt = time.Time{}
	}
	stable.Incidents = append([]domain.Memory(nil), pkt.Incidents...)
	for i := range stable.Incidents {
		stable.Incidents[i].CreatedAt = time.Time{}
		stable.Incidents[i].UpdatedAt = time.Time{}
	}

	b, err := json.Marshal(stable)
	if err != nil {
		return 0
	}
	return tokenize.Count(string(b))
}

// nodesByID returns a lookup of node ID to its graph node. The result is built
// once per Engine and cached, so repeated calls do not rebuild the map.
func (e *Engine) nodesByID() map[string]domain.Node {
	e.nodesByIDOnce.Do(func() {
		m := make(map[string]domain.Node, len(e.graph.Nodes))
		for _, n := range e.graph.Nodes {
			m[n.ID] = n
		}
		e.nodesByIDCache = m
	})
	return e.nodesByIDCache
}

// resolveNode finds a graph node by exact ID (FullName), simple name, or
// qualified-name tail. This mirrors intel.Resolve's strategy so the context
// engine accepts the same symbol names that kern graph/near/path do — not
// just exact FullNames. When multiple nodes share a simple name, the first
// (deterministic graph-node order) is returned; the caller can disambiguate
// with a qualified "Type.Method" for precision.
func (e *Engine) resolveNode(name string) (domain.Node, bool) {
	// 1. Exact ID match (FullName).
	for _, n := range e.graph.Nodes {
		if n.ID == name {
			return n, true
		}
	}
	// 2. Simple name match (Symbol.Name == name).
	for _, n := range e.graph.Nodes {
		if n.Symbol != nil && n.Symbol.Name == name {
			return n, true
		}
	}
	// 3. Qualified-name tail: "Type.Method" → try "Method" as simple name.
	if dot := strings.LastIndex(name, "."); dot >= 0 && dot < len(name)-1 {
		tail := name[dot+1:]
		for _, n := range e.graph.Nodes {
			if n.Symbol != nil && n.Symbol.Name == tail {
				return n, true
			}
		}
	}
	return domain.Node{}, false
}

// filesOf returns unique domain.File entries derived from the symbols, in
// deterministic (sorted-by-path) order. Each file collects the symbols it
// defines within the relevant set.
func filesOf(symbols []domain.Symbol) []domain.File {
	byPath := map[string]domain.File{}
	for _, s := range symbols {
		if s.File == "" {
			continue
		}
		f, ok := byPath[s.File]
		if !ok {
			f = domain.File{Path: s.File, Language: s.Language}
		}
		f.Symbols = append(f.Symbols, s)
		byPath[s.File] = f
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]domain.File, 0, len(paths))
	for _, p := range paths {
		out = append(out, byPath[p])
	}
	return out
}

// nodeIDs returns the ID of each node.
func nodeIDs(nodes []domain.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
