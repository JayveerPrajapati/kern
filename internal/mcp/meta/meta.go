// Package meta owns the kern_meta natural-language classifier and router
// (kern_meta) as plain functions. classifyMetaRequest and its sub-routers
// map a natural-language request to the kern_* tool that best answers it via
// deterministic keyword matching, with a dependency-free semantic fallback
// (semantic.go). The router itself is the deterministic front end of the
// `kern` meta-tool; the owning MCP server wires its handler dispatch through
// Hooks.
package meta

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/skills"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	// RouteTool dispatches a classified tool name to its handler. It must
	// route every tool in metaRoutedTools to the same handler an explicit
	// MCP call would reach; Handle rewrites any other name to kern_search
	// before calling RouteTool, so an unknown name is never dispatched.
	RouteTool func(ctx context.Context, name string, args map[string]any) (string, error)
	// ValidPhase reports whether p is one of the four agent phases
	// (adapter delegates to the server's validPhase). nil disables phase
	// validation.
	ValidPhase func(p string) bool
	// CostHint returns the deterministic est-latency/out-token hint for a
	// tool (adapter delegates to the server's costHintFor). nil omits the
	// cost line from the classified output.
	CostHint func(tool string) (estMs, estTokens int)
	// ToolCatalog renders the server's registered tool table (one line per
	// tool: name, phase, risk, one-line description). When non-nil, Handle
	// answers tool-catalog requests ("give me the full tool catalog") with
	// the rendered table instead of falling through to classification and
	// the symbol-search dead end. nil preserves the legacy behavior.
	ToolCatalog func() (string, error)
}

// metaRoutedTools is the exact set of tool names the legacy *Server.handleMeta
// switch dispatched. Names the classifier can produce but that have no
// dispatch arm (e.g. kern_fit_context) fall back to kern_search with the raw
// request as the query — Handle must preserve that behavior byte-for-byte.
var metaRoutedTools = map[string]bool{
	"kern_agent_coordination": true,
	"kern_agent_fingerprint":  true,
	"kern_agent_role_rbac":    true,
	"kern_analyze":            true,
	"kern_arch":               true,
	"kern_authorize_context":  true,
	"kern_audit":              true,
	"kern_bridges":            true,
	"kern_buddy":              true,
	"kern_churn":              true,
	"kern_cochange":           true,
	"kern_commitmsg":          true,
	"kern_communities":        true,
	"kern_compact_file":       true,
	"kern_compose":            true,
	"kern_context":            true,
	"kern_context_watch":      true,
	"kern_correlate":          true,
	"kern_cross_repo_impact":  true,
	"kern_cycles":             true,
	"kern_dead":               true,
	"kern_diff_files":         true,
	"kern_doc_search":         true,
	"kern_entry_points":       true,
	"kern_evidence_anchor":    true,
	"kern_exec":               true,
	"kern_explain":            true,
	"kern_explore":            true,
	"kern_frameworks":         true,
	"kern_graph":              true,
	"kern_health":             true,
	"kern_hubs":               true,
	"kern_impact":             true,
	"kern_incident":           true,
	"kern_inherits":           true,
	"kern_larges":             true,
	"kern_llm_providers":      true,
	"kern_mask_pii":           true,
	"kern_memory_ranked":      true,
	"kern_memory_recall":      true,
	"kern_modernize":          true,
	"kern_near":               true,
	"kern_onboard":            true,
	"kern_optimize_log":       true,
	"kern_optimize_output":    true,
	"kern_optimize_prompt":    true,
	"kern_pack":               true,
	"kern_path":               true,
	"kern_plan":               true,
	"kern_plan_context":       true,
	"kern_policy_dsl":         true,
	"kern_pre_edit":           true,
	"kern_probe":              true,
	"kern_project_map":        true,
	"kern_prompt_fill":        true,
	"kern_prose":              true,
	"kern_resolve":            true,
	"kern_retrieve":           true,
	"kern_review":             true,
	"kern_safe_delete":        true,
	"kern_schema_validate":    true,
	"kern_search":             true,
	"kern_security":           true,
	"kern_semantic_diff":      true,
	"kern_skill":              true,
	"kern_snapshot":           true,
	"kern_stats":              true,
	"kern_stream":             true,
	"kern_surprising":         true,
	"kern_test_gaps":          true,
	"kern_trace":              true,
	"kern_validate":           true,
	"kern_verify":             true,
	"kern_verify_output":      true,
	"kern_what_if":            true,
	"kern_why":                true,
}

// toolCatalogIntent reports whether the request asks for the tool catalog,
// so Handle can answer it with the server's registered tool table before
// classification turns it into a symbol search dead end (N1b). The phrases
// cover the live-verified dead-ends ("give me the full tool catalog",
// "what tools are available", "list the mcp tools", ...). Substring match
// on the lowercase request; the hook only fires for these explicit catalog
// phrasings — a question about one tool's behavior ("how does kern_search
// clip results") matches none of them and keeps its symbol routing.
func toolCatalogIntent(low string) bool {
	for _, phrase := range []string{
		"tool catalog", "catalog of tools", "tool list",
		"list all tools", "list the tools", "list tools",
		"what tools are available", "what tools do you have",
		"available tools", "full catalog", "which tools",
		"mcp tools", "show the tools", "show me the tools",
		"all tools", "tool inventory",
	} {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return false
}

// Handle implements the kern_meta tool body: it takes a natural-language
// request, classifies it via ClassifyMetaRequest, dispatches to the chosen
// tool through h.RouteTool, and returns the result prefixed with the
// classification. It mirrors the legacy *Server.handleMeta exactly: only the
// tools in metaRoutedTools are dispatched; any other name — including a
// semantic route to a tool without a dispatch arm — falls back to kern_search
// with the raw request as the query.
func Handle(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	request := mcpargs.ArgString(args, "request")
	if request == "" {
		return "", fmt.Errorf("request is required")
	}
	// Phase hint (P1.2): the caller declares which agent phase it is in. It
	// does not mutate server state — MCP's tools/list is stateless — so the
	// advertised surface is filtered server-wide via KERN_MCP_PHASE instead.
	// The hint is validated and echoed back so agents learn the env switch.
	phase := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "phase")))
	if phase != "" && h.ValidPhase != nil && !h.ValidPhase(phase) {
		return "", fmt.Errorf("phase must be one of explore|plan|edit|verify, got %q", phase)
	}
	root := mcpargs.ArgString(args, "root")
	// Tool-catalog intents (N1b): render the server's registered tool table
	// directly instead of falling through to classification and the
	// symbol-search dead end. Only fires when the server wired the hook;
	// with a nil hook the request classifies exactly as before.
	if h.ToolCatalog != nil && toolCatalogIntent(strings.ToLower(request)) {
		cat, err := h.ToolCatalog()
		if err != nil {
			return "", err
		}
		n := 0
		for _, ln := range strings.Split(cat, "\n") {
			if strings.TrimSpace(ln) != "" {
				n++
			}
		}
		return fmt.Sprintf("[kern] tool catalog (%d tools):\n%s", n, cat), nil
	}
	tool, subArgs := ClassifyMetaRequest(request)
	viaSemantic := false
	if v, _ := subArgs[ViaSemanticArg].(bool); v {
		delete(subArgs, ViaSemanticArg)
		viaSemantic = true
	}
	if root != "" {
		subArgs["root"] = root
	}
	// Forward the governed-mode agent context (P1.2) so kern_meta's routed
	// retrieval sub-tools can authorize: agent_id/task/scope reach the same
	// handlers an explicit kern_explore/kern_context/kern_graph call would.
	for _, k := range []string{"agent_id", "task", "scope"} {
		if v, ok := args[k]; ok {
			subArgs[k] = v
		}
	}
	// Dispatch to the chosen handler. The handlers all share the signature
	// func(ctx, args) (string, error) and live on the owning server.
	if !metaRoutedTools[tool] {
		// Fallback: search
		subArgs["query"] = request
		tool = "kern_search"
	}
	if h.RouteTool == nil {
		return "", fmt.Errorf("meta: RouteTool hook is not wired")
	}
	result, err := h.RouteTool(ctx, tool, subArgs)
	if err != nil {
		return "", err
	}
	out := "[kern] classified as: " + tool
	if viaSemantic {
		out += " (semantic fallback)"
	}
	if h.CostHint != nil {
		estMs, estTokens := h.CostHint(tool)
		out += fmt.Sprintf(" · est %dms · %d out tokens", estMs, estTokens)
	}
	out += "\n" + result
	if phase != "" {
		out += fmt.Sprintf("\n[phase hint: %s — set KERN_MCP_PHASE=%s to filter the advertised tool list]", phase, phase)
	}
	return out, nil
}

// VerifyTypesExec reports whether any requested verification type executes
// host commands, mirroring the engine's Verify dispatch (substring match).
// The engine runs build, test (unit/integration), e2e, static-analysis
// (vet/lint) and performance (bench) through validate.Run / sandbox.Run —
// arbitrary host code — and the CI check through its adapter (gh et al).
// cve shells out to govulncheck and secrets to `git log`, so both are host
// command execution too. Architecture, security, dependency and license are
// in-process (index scans, sec rules, manifest parsing) and never shell out,
// so a request limited to those types must NOT require the exec allowlist.
func VerifyTypesExec(types []string) bool {
	if len(types) == 0 {
		return true // engine default runs build+test: exec
	}
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		switch {
		case strings.Contains(t, "cve"):
			return true
		case strings.Contains(t, "secret"):
			return true
		case strings.Contains(t, "build"):
			return true
		case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
			return true
		case strings.Contains(t, "e2e"), strings.Contains(t, "end-to-end"):
			return true
		case strings.Contains(t, "static"), strings.Contains(t, "analysis"), strings.Contains(t, "vet"), strings.Contains(t, "lint"):
			return true
		case strings.Contains(t, "perf"), strings.Contains(t, "bench"):
			return true
		case strings.Contains(t, "ci"):
			return true
		}
	}
	return false
}

// VerifyTypesKnown are the canonical verification types the unified engine
// accepts (verification.Engine.Verify, substring dispatch). A request token is
// valid only when it matches one of them; anything else (e.g. a number
// coerced to "123") is rejected up front so a garbage types list can never
// degrade into a vacuous "summary: PASS" run where every sub-check is
// silently skipped.
var VerifyTypesKnown = []string{"build", "test", "security", "architecture", "dependency", "e2e", "static-analysis", "performance", "cve", "license", "secrets", "ci"}

// KnownVerifyType reports whether a token names a verification the engine can
// run. It mirrors verification.Engine.Verify's substring dispatch exactly, so
// valid aliases the engine accepts (unit/integration for test, vet/lint for
// static-analysis, sec for security, dep for dependency, bench for
// performance) stay accepted and only unrecognized garbage is rejected.
func KnownVerifyType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case strings.Contains(t, "cve"):
		return true
	case strings.Contains(t, "licen"):
		return true
	case strings.Contains(t, "secret"):
		return true
	case strings.Contains(t, "build"):
		return true
	case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
		return true
	case strings.Contains(t, "security"), strings.Contains(t, "sec"):
		return true
	case strings.Contains(t, "archi"):
		return true
	case strings.Contains(t, "depend"), strings.Contains(t, "dep"):
		return true
	case strings.Contains(t, "e2e"), strings.Contains(t, "end-to-end"):
		return true
	case strings.Contains(t, "static"), strings.Contains(t, "analysis"), strings.Contains(t, "vet"), strings.Contains(t, "lint"):
		return true
	case strings.Contains(t, "perf"), strings.Contains(t, "bench"):
		return true
	case strings.Contains(t, "ci"):
		return true
	}
	return false
}

// ValidateVerifyTypes rejects any requested verification type the engine
// cannot run. It must run BEFORE the exec firewall and before any check, so a
// garbage types list (types=123 coerced to "123") errors out instead of
// producing a vacuous PASS.
func ValidateVerifyTypes(types []string) error {
	for _, t := range types {
		if !KnownVerifyType(t) {
			return fmt.Errorf("unknown verify type: %s (known: %s)", t, strings.Join(VerifyTypesKnown, ", "))
		}
	}
	return nil
}

// ClassifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
// ExtractSymbol pulls a candidate symbol name from a natural-language request:
// quoted text ("dispatch" or `dispatch`), or a CamelCase / dotted identifier
// token. It mirrors the legacy closure that lived inside classifyMetaRequest.
func ExtractSymbol(request, low string) string {
	// Quoted: "dispatch" or `dispatch`
	if i := strings.IndexAny(low, "\"`"); i >= 0 {
		q := low[i]
		j := strings.IndexByte(low[i+1:], q)
		if j > 0 {
			return request[i+1 : i+1+j]
		}
	}
	// CamelCase token: dispatch, Server.dispatch, NewServer
	for _, word := range strings.Fields(request) {
		w := strings.Trim(word, ".,;:!?()[]{}\"`'")
		if w == "" {
			continue
		}
		// Contains a dot (qualified) or has mixed case (CamelCase) and looks like an ident
		if strings.Contains(w, ".") {
			return w
		}
		hasUpper, hasLower := false, false
		for _, r := range w {
			if r >= 'A' && r <= 'Z' {
				hasUpper = true
			}
			if r >= 'a' && r <= 'z' {
				hasLower = true
			}
		}
		if hasUpper && hasLower && len(w) > 2 {
			return w
		}
	}
	return ""
}

// ExtractAfterColon returns the text after the first colon in the request, or
// the whole request when there is none. Used by log/mask/compress-type tools
// that receive their payload inline after a colon.
func ExtractAfterColon(request, low string) string {
	if i := strings.Index(low, ":"); i >= 0 && i+1 < len(request) {
		return strings.TrimSpace(request[i+1:])
	}
	return request
}

// WithSymbol attaches the symbol extracted from the request to args, or
// reroutes to the fallback tool with the full request as its query when no
// symbol is present. A non-empty fallback is required for the reroute; a tool
// that tolerates a missing symbol passes fallback="" and keeps its governance.
func WithSymbol(request, low, tool, fallback string, args map[string]any) (string, map[string]any) {
	if sym := ExtractSymbol(request, low); sym != "" {
		args["symbol"] = sym
		return tool, args
	}
	if fallback != "" {
		args["query"] = request
		return fallback, args
	}
	return tool, args
}

// howWhyRe captures the bare symbol in "how does X work?" style questions:
// the first word after the how-does/how-do/why-does phrase, optionally
// preceded by an article ("how does the index work" -> "index"). Lowercase
// start only — CamelCase/dotted symbols are already handled by ExtractSymbol.
var howWhyRe = regexp.MustCompile(`\b(?:how does|how do|why does)\s+(?:(?:the|a|an)\s+)?([a-z][a-z0-9_.]*)`)

// howWhySymbol extracts the bare lowercase symbol from "how does X work?"
// style questions, which ExtractSymbol deliberately misses (it only pulls
// quoted/dotted/CamelCase tokens). The generic template words
// ("work", "function", "behave", "works") are not symbols — a question like
// "how does work happen?" must not invent a "work" symbol. Returns "" when
// there is no plausible symbol, so the caller falls back to kern_search.
func howWhySymbol(low string) string {
	m := howWhyRe.FindStringSubmatch(low)
	if m == nil {
		return ""
	}
	switch m[1] {
	case "work", "function", "behave", "works":
		return ""
	}
	return m[1]
}

// statusSymbol extracts the symbol named by a "status of X" request — the
// token immediately after the "status of" phrase in the ORIGINAL request —
// and reports it only when it looks symbol-like (dot-qualified or CamelCase,
// the same conventions ExtractSymbol uses; a single uppercase letter like
// "B" also counts — a valid one-character symbol). Leading articles are
// skipped so "status of the TaskService" still finds the symbol, while
// "status of the project", "status of the index" and "build status" yield ""
// and keep the kern_health routing. Returns "" when the request carries no
// "status of" phrase at all.
func statusSymbol(request string) string {
	i := strings.Index(strings.ToLower(request), "status of ")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(request[i+len("status of "):])
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(strings.ToLower(rest), art) {
			rest = strings.TrimSpace(rest[len(art):])
			break
		}
	}
	if rest == "" {
		return ""
	}
	// First token, punctuation-stripped exactly like ExtractSymbol.
	w := strings.Trim(strings.Fields(rest)[0], ".,;:!?()[]{}\"`'")
	if w == "" {
		return ""
	}
	if strings.Contains(w, ".") {
		return w
	}
	// A single uppercase letter is a valid symbol ("status of B").
	if len(w) == 1 && w[0] >= 'A' && w[0] <= 'Z' {
		return w
	}
	hasUpper, hasLower := false, false
	for _, r := range w {
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		}
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
	}
	if hasUpper && hasLower && len(w) > 2 {
		return w
	}
	return ""
}

// wordReCache caches compiled word-boundary regexes per keyword.
var wordReCache sync.Map

// HasWord reports whether kw occurs in s as a standalone word, so camelCase
// symbol names (buildSecurityProperties) cannot hijack keyword routing.
func HasWord(s, kw string) bool {
	if kw == "" {
		return false
	}
	if v, ok := wordReCache.Load(kw); ok {
		return v.(*regexp.Regexp).MatchString(s)
	}
	re := regexp.MustCompile("\\b" + regexp.QuoteMeta(kw) + "\\b")
	wordReCache.Store(kw, re)
	return re.MatchString(s)
}

// ClassifyOptimizeTools routes the safety/PII and prompt/log compress cases.
func ClassifyOptimizeTools(low, request string) (string, map[string]any, bool) {
	switch {
	case HasWord(low, "mask") && (HasWord(low, "secret") || HasWord(low, "pii")):
		return "kern_mask_pii", map[string]any{"text": ExtractAfterColon(request, low)}, true
	case HasWord(low, "compress") && HasWord(low, "log"):
		return "kern_optimize_log", map[string]any{"log": ExtractAfterColon(request, low)}, true
	case HasWord(low, "compress") && (HasWord(low, "output") || HasWord(low, "response") || HasWord(low, "reply")):
		return "kern_optimize_output", map[string]any{"text": ExtractAfterColon(request, low)}, true
	case HasWord(low, "compress") && HasWord(low, "prompt"):
		return "kern_optimize_prompt", map[string]any{"prompt": ExtractAfterColon(request, low)}, true
	case HasWord(low, "security") || HasWord(low, "scan") && strings.Contains(low, "vulnerab"):
		return "kern_security", map[string]any{}, true
	case HasWord(low, "schema") || strings.Contains(low, "validate json"):
		return "kern_schema_validate", map[string]any{}, true
	}
	return "", nil, false
}

// ClassifyWorkflowTools routes the high-level orchestration cases.
func ClassifyWorkflowTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "what if") || HasWord(low, "simulate") || strings.Contains(low, "remove symbol"):
		return "kern_what_if", map[string]any{"change": request}, true
	case strings.Contains(low, "what breaks") || HasWord(low, "impact") || (HasWord(low, "change") && !HasWord(low, "analyze")):
		return "kern_impact", map[string]any{"change": request}, true
	case HasWord(low, "analyze") || HasWord(low, "propose"):
		return "kern_analyze", map[string]any{"change": request}, true
	case HasWord(low, "plan") && !strings.Contains(low, "implementation plan"):
		return "kern_plan", map[string]any{"change": request}, true
	case HasWord(low, "incident"):
		return "kern_incident", map[string]any{}, true
	case HasWord(low, "correlate"):
		return "kern_correlate", map[string]any{}, true
	case strings.Contains(low, "modernize"):
		return "kern_modernize", map[string]any{}, true
	case HasWord(low, "verify") && strings.Contains(low, "claim"):
		return "kern_verify_output", map[string]any{"text": ExtractAfterColon(request, low)}, true
	case HasWord(low, "verify"):
		return "kern_verify", map[string]any{}, true
	case strings.Contains(low, "architecture narrat") || strings.Contains(low, "narrat") || (HasWord(low, "explain") && HasWord(low, "architecture")):
		return "kern_explain", map[string]any{"target": ExtractSymbol(request, low)}, true
	case strings.Contains(low, "cross repo") || strings.Contains(low, "multi repo"):
		return "kern_cross_repo_impact", map[string]any{"target_symbol": ExtractSymbol(request, low)}, true
	case strings.Contains(low, "ranked memor") || strings.Contains(low, "decay memor"):
		return "kern_memory_ranked", map[string]any{"prompt": request}, true
	case strings.Contains(low, "policy dsl") || strings.Contains(low, "evaluate policy"):
		return "kern_policy_dsl", map[string]any{}, true
	case strings.Contains(low, "agent coordination") || (HasWord(low, "coordination") && strings.Contains(low, "agent")):
		return "kern_agent_coordination", map[string]any{"action": "status"}, true
	case strings.Contains(low, "rbac") || strings.Contains(low, "agent role"):
		return "kern_agent_role_rbac", map[string]any{"action": "roles"}, true
	case strings.Contains(low, "stream chunk") || (HasWord(low, "stream") && HasWord(low, "transport")):
		return "kern_stream", map[string]any{"action": "status"}, true
	}
	return "", nil, false
}

// ClassifyArchTools routes the architecture/subsystem inspection cases.
func ClassifyArchTools(low, request string) (string, map[string]any, bool) {
	switch {
	case HasWord(low, "architecture") || HasWord(low, "overview") || HasWord(low, "subsystem"):
		return "kern_arch", map[string]any{}, true
	case strings.Contains(low, "communit") || HasWord(low, "cluster"):
		return "kern_communities", map[string]any{}, true
	case strings.Contains(low, "surprising") || strings.Contains(low, "surprise") || strings.Contains(low, "unexpected connection"):
		return "kern_surprising", map[string]any{}, true
	case strings.Contains(low, "snapshot"):
		return "kern_snapshot", map[string]any{}, true
	case strings.Contains(low, "cycle") || strings.Contains(low, "import graph") || strings.Contains(low, "circular"):
		return "kern_cycles", map[string]any{}, true
	case HasWord(low, "hub") || HasWord(low, "hotspot") || strings.Contains(low, "most depended"):
		return "kern_hubs", map[string]any{}, true
	case HasWord(low, "bridge") || HasWord(low, "coupling"):
		return "kern_bridges", map[string]any{}, true
	case strings.Contains(low, "dead code") || HasWord(low, "unused"):
		return "kern_dead", map[string]any{}, true
	case HasWord(low, "largest") || strings.Contains(low, "god function") || HasWord(low, "biggest"):
		return "kern_larges", map[string]any{}, true
	case strings.Contains(low, "test gap") || HasWord(low, "coverage"):
		return "kern_test_gaps", map[string]any{}, true
	case strings.Contains(low, "entry point") || HasWord(low, "handler") || HasWord(low, "route"):
		return "kern_entry_points", map[string]any{}, true
	case HasWord(low, "framework") || HasWord(low, "library") || strings.Contains(low, "detect stack"):
		return "kern_frameworks", map[string]any{}, true
	case HasWord(low, "churn") || strings.Contains(low, "changed most"):
		return "kern_churn", map[string]any{}, true
	case strings.Contains(low, "cochange") || strings.Contains(low, "co-change") || strings.Contains(low, "lockstep"):
		return "kern_cochange", map[string]any{}, true
	case HasWord(low, "diff") && (strings.Contains(low, "file") || HasWord(low, "compare")):
		return "kern_diff_files", map[string]any{}, true
	case HasWord(low, "review") || strings.Contains(low, "pr "):
		return "kern_review", map[string]any{}, true
	}
	return "", nil, false
}

// ClassifyGovernanceTools routes the authorized-context case; it must be
// consulted after the architecture cases and before the symbol-level graph
// cases to preserve the original switch's precedence.
func ClassifyGovernanceTools(low, request string) (string, map[string]any, bool) {
	switch {
	case HasWord(low, "authorize") || HasWord(low, "authorized") ||
		strings.Contains(low, "allowed to see") || strings.Contains(low, "permitted") ||
		strings.Contains(low, "what can i"):
		return "kern_authorize_context", map[string]any{"task": request}, true
	// Audit intents (N1a): "audit log/trail/entries/history" and the explicit
	// "show the audit" / "audit what happened" phrasings route to kern_audit.
	// The word-boundary check keeps "AuditLog" (one word — a symbol) out:
	// "how does AuditLog work" must stay a symbol question, not an audit
	// command.
	case HasWord(low, "audit") && (HasWord(low, "log") || HasWord(low, "trail") || HasWord(low, "entries") || HasWord(low, "history")) ||
		strings.Contains(low, "show the audit") || strings.Contains(low, "audit what happened"):
		return "kern_audit", map[string]any{}, true
	}
	return "", nil, false
}

// ClassifyGraphTools routes the symbol-level graph/explore cases, extracting a
// symbol from the request when one is present and falling back to kern_search.
func ClassifyGraphTools(low, request string) (string, map[string]any, bool) {
	switch {
	// Flow questions ("how does the bundle upload flow work end to end?")
	// name a process, not a single symbol: walk the dependency tree from the
	// named symbol when one is extractable, otherwise answer with the
	// system's entry points (handlers/routes) instead of falling through to a
	// flat kern_search list.
	case strings.Contains(low, "flow") || strings.Contains(low, "workflow") || strings.Contains(low, "pipeline") || strings.Contains(low, "end to end") || strings.Contains(low, "end-to-end"):
		tool, args := WithSymbol(request, low, "kern_near", "kern_entry_points", map[string]any{})
		if tool == "kern_near" {
			args["depth"] = "4"
		}
		return tool, args, true
	case strings.Contains(low, "how does") || HasWord(low, "understand") || HasWord(low, "explain"):
		// ExtractSymbol only pulls quoted/dotted/CamelCase tokens, so a bare
		// lowercase symbol ("dispatch") never matches it. Fall back to the
		// "how does X work" shape before giving up to kern_search.
		if sym := ExtractSymbol(request, low); sym != "" {
			return "kern_explore", map[string]any{"symbol": sym}, true
		}
		if sym := howWhySymbol(low); sym != "" {
			return "kern_explore", map[string]any{"symbol": sym}, true
		}
		tool, args := WithSymbol(request, low, "kern_explore", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "why does") || strings.Contains(low, "why is") || strings.Contains(low, "rationale"):
		if sym := ExtractSymbol(request, low); sym != "" {
			return "kern_why", map[string]any{"symbol": sym}, true
		}
		if sym := howWhySymbol(low); sym != "" {
			return "kern_why", map[string]any{"symbol": sym}, true
		}
		tool, args := WithSymbol(request, low, "kern_why", "kern_search", map[string]any{})
		return tool, args, true
	case HasWord(low, "callers") || strings.Contains(low, "who calls") || strings.Contains(low, "call graph"):
		tool, args := WithSymbol(request, low, "kern_graph", "kern_search", map[string]any{"format": "one-line"})
		return tool, args, true
	case strings.Contains(low, "inherit") || HasWord(low, "hierarchy") || HasWord(low, "extends") || HasWord(low, "implements"):
		tool, args := WithSymbol(request, low, "kern_inherits", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "path from") || strings.Contains(low, "call path") || strings.Contains(low, "shortest path"):
		return "kern_path", map[string]any{}, true
	case HasWord(low, "near") || strings.Contains(low, "depends on") || strings.Contains(low, "neighborhood"):
		tool, args := WithSymbol(request, low, "kern_near", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "context for") || strings.Contains(low, "source slice") || strings.Contains(low, "source for"):
		tool, args := WithSymbol(request, low, "kern_context", "kern_search", map[string]any{})
		return tool, args, true
	case HasWord(low, "trace") && (strings.Contains(low, "stack") || strings.Contains(low, "pprof")):
		return "kern_trace", map[string]any{}, true
	case HasWord(low, "probe") || strings.Contains(low, "what does this touch"):
		return "kern_probe", map[string]any{"task": request}, true
	case strings.Contains(low, "prose") || strings.Contains(low, "vocab") || strings.Contains(low, "spelling"):
		// prose-word → symbol candidate lookup; kept after the more
		// specific symbol questions so "explain the vocab" still explores.
		return "kern_prose", map[string]any{"query": request}, true
	}
	return "", nil, false
}

// ClassifyProjectTools routes the project-level utility cases.
func ClassifyProjectTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "project map") || HasWord(low, "layout") || HasWord(low, "structure") && HasWord(low, "project"):
		return "kern_project_map", map[string]any{}, true
	case strings.Contains(low, "pack") || strings.Contains(low, "bundle"):
		return "kern_pack", map[string]any{}, true
	case strings.Contains(low, "fit context") || strings.Contains(low, "adaptive context") || strings.Contains(low, "compress context") || strings.Contains(low, "fit token"):
		return "kern_fit_context", map[string]any{"query": request}, true
	case HasWord(low, "compact") && strings.Contains(low, "file"):
		return "kern_compact_file", map[string]any{}, true
	// Index intents (dogfood F-2): "rebuild/refresh the index" has no
	// dedicated MCP tool — kern_onboard is the tool that registers and
	// builds/refreshes the index; index status/freshness questions are
	// answered by kern_health (which now falls back to the disk view).
	// Checked before the buddy/onboard branch so "index ... onboard" style
	// phrasings still land on the index intent, and before the health branch
	// so "index status" is unambiguous.
	case strings.Contains(low, "index") && (strings.Contains(low, "rebuild") || strings.Contains(low, "refresh") || HasWord(low, "reindex") || strings.Contains(low, "build the index")):
		return "kern_onboard", map[string]any{}, true
	case strings.Contains(low, "index") && (HasWord(low, "status") || HasWord(low, "fresh") || HasWord(low, "stale") || HasWord(low, "health")):
		return "kern_health", map[string]any{}, true
	// LLM provider intents (dogfood): chain/sampler/status questions are
	// answered by kern_llm_providers ("which local agent should I use");
	// plain code questions ("how does the llm provider work") stay searches.
	case (strings.Contains(low, "llm") && (strings.Contains(low, "providers") || strings.Contains(low, "chain") || strings.Contains(low, "sampler") || strings.Contains(low, "sampling") || HasWord(low, "status"))) ||
		(strings.Contains(low, "host") && (strings.Contains(low, "sampler") || strings.Contains(low, "sampling"))) ||
		(HasWord(low, "agent") && HasWord(low, "use") && (HasWord(low, "local") || HasWord(low, "which"))):
		return "kern_llm_providers", map[string]any{}, true
	case HasWord(low, "buddy") || HasWord(low, "onboard") || strings.Contains(low, "getting started"):
		return "kern_buddy", map[string]any{}, true
	// Symbol status intents (N8): "status of X" / "what's the status of X" /
	// "what is the status of X" where X is a symbol (dot-qualified or
	// CamelCase in the ORIGINAL request) route to kern_explore — a symbol's
	// "status" is its definition + callers + blast radius. The guard runs
	// before the generic health branch so "status of Server.dispatch" no
	// longer lands on project-health JSON; non-symbol status phrasings
	// ("status of the project", "build status") yield "" and keep the
	// kern_health routing below.
	case statusSymbol(request) != "":
		return "kern_explore", map[string]any{"symbol": statusSymbol(request)}, true
	case HasWord(low, "health") || HasWord(low, "status") || strings.Contains(low, "self-check") || strings.Contains(low, "diagnose"):
		return "kern_health", map[string]any{}, true
	case HasWord(low, "stats") || HasWord(low, "savings") || strings.Contains(low, "token count"):
		return "kern_stats", map[string]any{}, true
	case strings.Contains(low, "commit message") || strings.Contains(low, "commitmsg"):
		return "kern_commitmsg", map[string]any{}, true
	case HasWord(low, "memory") || HasWord(low, "remember") || HasWord(low, "lesson"):
		return "kern_memory_recall", map[string]any{"prompt": request}, true
	case HasWord(low, "docs") || HasWord(low, "documentation"):
		return "kern_doc_search", map[string]any{"query": request}, true
	case HasWord(low, "build") || HasWord(low, "test") || HasWord(low, "lint"):
		return "kern_validate", map[string]any{}, true
	case strings.Contains(low, "repair") || strings.Contains(low, "auto repair") || strings.Contains(low, "fix diagnostics") || strings.Contains(low, "compiler error"):
		return "kern_repair_diagnostics", map[string]any{"compiler_output": request}, true
	case HasWord(low, "exec") || strings.Contains(low, "run script") || strings.Contains(low, "run code"):
		return "kern_exec", map[string]any{}, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := WithSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := WithSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "rename") || strings.Contains(low, "refactor name"):
		return "kern_rename", map[string]any{}, true
	}
	return "", nil, false
}

// ClassifyRetrievalTools routes the progressive-disclosure retrieval cases
// (P1/P2/P3 tracker): kern_retrieve, kern_resolve and kern_plan_context. It
// is consulted BEFORE the workflow/arch/graph routers so "plan context",
// "retrieve ..." and "resolve ..." requests beat the broader "plan"/"handler"
// keywords those routers claim. "handle" is matched only as a standalone word
// (never inside "handler", which stays kern_entry_points).
func ClassifyRetrievalTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "resolve"):
		// "resolve handle <id>" / "resolve <id>": extract the trailing token
		// as the handle; without one, pass the request through so the handler
		// rejects it with its own "handle is required" error.
		id := ""
		rest := low
		if i := strings.Index(rest, "resolve"); i >= 0 {
			rest = strings.TrimSpace(rest[i+len("resolve"):])
		}
		if j := strings.Index(rest, "handle"); j >= 0 {
			rest = strings.TrimSpace(rest[j+len("handle"):])
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			id = fields[0]
		}
		if id == "" {
			id = request
		}
		return "kern_resolve", map[string]any{"handle": id}, true
	case (strings.Contains(low, "retrieve") || strings.Contains(low, "handle")) && !HasWord(low, "handler"):
		// NL requests name symbols, not structured handles, so land on the
		// L1 name/token-cost list for the whole request as the query.
		return "kern_retrieve", map[string]any{"query": request, "level": "l1"}, true
	case strings.Contains(low, "context plan") || strings.Contains(low, "plan context") || strings.Contains(low, "explain context") || strings.Contains(low, "planner"):
		return "kern_plan_context", map[string]any{"change": request}, true
	case strings.Contains(low, "orchestrate") || strings.Contains(low, "silent context") || strings.Contains(low, "context pipeline"):
		return "kern_orchestrate", map[string]any{"intent": request}, true
	case strings.Contains(low, "agent skills") || strings.Contains(low, "list skills") || strings.Contains(low, "load skill") || strings.Contains(low, "skill runbook"):
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	return "", nil, false
}

// ClassifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
// ClassifySkillTools routes explicit skill-language queries (runbook,
// playbook, a literal skill name, or "skill" with load/use/show intent) to
// kern_skill before the workflow router can claim them. Semantic phrases
// WITHOUT skill language deliberately stay un-routed: "make a safe change"
// -> kern_impact and "triage this incident" -> kern_incident are better
// answers than loading the runbook.
func ClassifySkillTools(low, request string) (string, map[string]any, bool) {
	if strings.Contains(low, "runbook") || strings.Contains(low, "playbook") {
		name := ""
		for _, n := range skills.SkillNames {
			if strings.Contains(low, n) || strings.Contains(low, strings.TrimPrefix(n, "kern-")) {
				name = n
				break
			}
		}
		if name != "" {
			return "kern_skill", map[string]any{"action": "load", "skill": name}, true
		}
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	if strings.Contains(low, "incident triage") {
		return "kern_skill", map[string]any{"action": "load", "skill": "kern-incident-triage"}, true
	}
	if strings.Contains(low, "safe change") && (strings.Contains(low, "skill") || strings.Contains(low, "runbook") || strings.Contains(low, "playbook")) {
		return "kern_skill", map[string]any{"action": "load", "skill": "kern-safe-change"}, true
	}
	if strings.Contains(low, "what skills") || strings.Contains(low, "available skills") {
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	if HasWord(low, "skill") && (strings.Contains(low, "load") || strings.Contains(low, "use") || strings.Contains(low, "show") || strings.Contains(low, "list")) {
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	return "", nil, false
}

// ClassifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
func ClassifyMetaRequest(request string) (string, map[string]any) {
	low := strings.ToLower(request)
	// The sub-routers are consulted in the same order as the original
	// monolithic switch (safety/optimize -> workflows -> architecture ->
	// governance -> symbol graph -> project), so classification outcomes are
	// unchanged; anything unmatched still falls back to kern_search. The
	// retrieval router is consulted FIRST so its specific phrases (plan
	// context, retrieve/resolve) beat the broader workflow/arch keywords.
	if t, a, ok := ClassifyRetrievalTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifySkillTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifyOptimizeTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifyWorkflowTools(low, request); ok {
		return t, a
	}
	// CLI/subcommand questions ("how does CLI command dispatch work?") are
	// symbol searches, not architecture "entry point" questions — guard them
	// before the graph router can fall back to kern_entry_points (report F-1).
	// Workflow verbs (impact/analyze/plan) already won above, so "what breaks
	// if I change the CLI dispatch table" still routes to kern_impact; and a
	// dotted qualified symbol (Server.dispatch) skips this guard so
	// "how does Server.dispatch work" still routes to kern_explore.
	if (strings.Contains(low, "cli") || strings.Contains(low, "subcommand") || strings.Contains(low, "command dispatch")) &&
		!strings.Contains(low, ".") {
		return "kern_search", map[string]any{"query": request}
	}
	if t, a, ok := ClassifyArchTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifyGovernanceTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifyGraphTools(low, request); ok {
		return t, a
	}
	if t, a, ok := ClassifyProjectTools(low, request); ok {
		return t, a
	}
	if t, a, _, ok := SemanticMetaRoute(request); ok {
		a[ViaSemanticArg] = true
		return t, a
	}
	return "kern_search", map[string]any{"query": request}
}
