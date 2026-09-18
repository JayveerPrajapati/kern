package mcp

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/skills"
)

// verifyTypesExec reports whether any requested verification type executes
// host commands, mirroring the engine's Verify dispatch (substring match).
// The engine runs build, test (unit/integration), e2e, static-analysis
// (vet/lint) and performance (bench) through validate.Run / sandbox.Run —
// arbitrary host code — and the CI check through its adapter (gh et al).
// Architecture, security and dependency are in-process (index scans, sec
// rules, manifest parsing) and never shell out, so a request limited to
// those types must NOT require the exec allowlist.
func verifyTypesExec(types []string) bool {
	if len(types) == 0 {
		return true // engine default runs build+test: exec
	}
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		switch {
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

// verifyTypesKnown are the canonical verification types the unified engine
// accepts (verification.Engine.Verify, substring dispatch). A request token is
// valid only when it matches one of them; anything else (e.g. a number
// coerced to "123") is rejected up front so a garbage types list can never
// degrade into a vacuous "summary: PASS" run where every sub-check is
// silently skipped.
var verifyTypesKnown = []string{"build", "test", "security", "architecture", "dependency", "e2e", "static-analysis", "performance", "ci"}

// knownVerifyType reports whether a token names a verification the engine can
// run. It mirrors verification.Engine.Verify's substring dispatch exactly, so
// valid aliases the engine accepts (unit/integration for test, vet/lint for
// static-analysis, sec for security, dep for dependency, bench for
// performance) stay accepted and only unrecognized garbage is rejected.
func knownVerifyType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
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

// validateVerifyTypes rejects any requested verification type the engine
// cannot run. It must run BEFORE the exec firewall and before any check, so a
// garbage types list (types=123 coerced to "123") errors out instead of
// producing a vacuous PASS.
func validateVerifyTypes(types []string) error {
	for _, t := range types {
		if !knownVerifyType(t) {
			return fmt.Errorf("unknown verify type: %s (known: %s)", t, strings.Join(verifyTypesKnown, ", "))
		}
	}
	return nil
}

// classifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
// extractSymbol pulls a candidate symbol name from a natural-language request:
// quoted text ("dispatch" or `dispatch`), or a CamelCase / dotted identifier
// token. It mirrors the legacy closure that lived inside classifyMetaRequest.
func extractSymbol(request, low string) string {
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

// extractAfterColon returns the text after the first colon in the request, or
// the whole request when there is none. Used by log/mask/compress-type tools
// that receive their payload inline after a colon.
func extractAfterColon(request, low string) string {
	if i := strings.Index(low, ":"); i >= 0 && i+1 < len(request) {
		return strings.TrimSpace(request[i+1:])
	}
	return request
}

// withSymbol attaches the symbol extracted from the request to args, or
// reroutes to the fallback tool with the full request as its query when no
// symbol is present. A non-empty fallback is required for the reroute; a tool
// that tolerates a missing symbol passes fallback="" and keeps its governance.
func withSymbol(request, low, tool, fallback string, args map[string]any) (string, map[string]any) {
	if sym := extractSymbol(request, low); sym != "" {
		args["symbol"] = sym
		return tool, args
	}
	if fallback != "" {
		args["query"] = request
		return fallback, args
	}
	return tool, args
}

// wordReCache caches compiled word-boundary regexes per keyword.
var wordReCache sync.Map

// hasWord reports whether kw occurs in s as a standalone word, so camelCase
// symbol names (buildSecurityProperties) cannot hijack keyword routing.
func hasWord(s, kw string) bool {
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

// classifyOptimizeTools routes the safety/PII and prompt/log compress cases.
func classifyOptimizeTools(low, request string) (string, map[string]any, bool) {
	switch {
	case hasWord(low, "mask") && (hasWord(low, "secret") || hasWord(low, "pii")):
		return "kern_mask_pii", map[string]any{"text": extractAfterColon(request, low)}, true
	case hasWord(low, "compress") && hasWord(low, "log"):
		return "kern_optimize_log", map[string]any{"log": extractAfterColon(request, low)}, true
	case hasWord(low, "compress") && (hasWord(low, "output") || hasWord(low, "response") || hasWord(low, "reply")):
		return "kern_optimize_output", map[string]any{"text": extractAfterColon(request, low)}, true
	case hasWord(low, "compress") && hasWord(low, "prompt"):
		return "kern_optimize_prompt", map[string]any{"prompt": extractAfterColon(request, low)}, true
	case hasWord(low, "security") || hasWord(low, "scan") && strings.Contains(low, "vulnerab"):
		return "kern_security", map[string]any{}, true
	case hasWord(low, "schema") || strings.Contains(low, "validate json"):
		return "kern_schema_validate", map[string]any{}, true
	}
	return "", nil, false
}

// classifyWorkflowTools routes the high-level orchestration cases.
func classifyWorkflowTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "what if") || hasWord(low, "simulate") || strings.Contains(low, "remove symbol"):
		return "kern_what_if", map[string]any{"change": request}, true
	case strings.Contains(low, "what breaks") || hasWord(low, "impact") || (hasWord(low, "change") && !hasWord(low, "analyze")):
		return "kern_impact", map[string]any{"change": request}, true
	case hasWord(low, "analyze") || hasWord(low, "propose"):
		return "kern_analyze", map[string]any{"change": request}, true
	case hasWord(low, "plan") && !strings.Contains(low, "implementation plan"):
		return "kern_plan", map[string]any{"change": request}, true
	case hasWord(low, "incident"):
		return "kern_incident", map[string]any{}, true
	case hasWord(low, "correlate"):
		return "kern_correlate", map[string]any{}, true
	case strings.Contains(low, "modernize"):
		return "kern_modernize", map[string]any{}, true
	case hasWord(low, "verify") && strings.Contains(low, "claim"):
		return "kern_verify_output", map[string]any{"text": extractAfterColon(request, low)}, true
	case hasWord(low, "verify"):
		return "kern_verify", map[string]any{}, true
	case strings.Contains(low, "architecture narrat") || strings.Contains(low, "narrat") || (hasWord(low, "explain") && hasWord(low, "architecture")):
		return "kern_explain", map[string]any{"target": extractSymbol(request, low)}, true
	case strings.Contains(low, "cross repo") || strings.Contains(low, "multi repo"):
		return "kern_cross_repo_impact", map[string]any{"target_symbol": extractSymbol(request, low)}, true
	case strings.Contains(low, "ranked memor") || strings.Contains(low, "decay memor"):
		return "kern_memory_ranked", map[string]any{"prompt": request}, true
	case strings.Contains(low, "policy dsl") || strings.Contains(low, "evaluate policy"):
		return "kern_policy_dsl", map[string]any{}, true
	case strings.Contains(low, "agent coordination") || (hasWord(low, "coordination") && strings.Contains(low, "agent")):
		return "kern_agent_coordination", map[string]any{"action": "status"}, true
	case strings.Contains(low, "rbac") || strings.Contains(low, "agent role"):
		return "kern_agent_role_rbac", map[string]any{"action": "roles"}, true
	case strings.Contains(low, "stream chunk") || (hasWord(low, "stream") && hasWord(low, "transport")):
		return "kern_stream", map[string]any{"action": "status"}, true
	}
	return "", nil, false
}

// classifyArchTools routes the architecture/subsystem inspection cases.
func classifyArchTools(low, request string) (string, map[string]any, bool) {
	switch {
	case hasWord(low, "architecture") || hasWord(low, "overview") || hasWord(low, "subsystem"):
		return "kern_arch", map[string]any{}, true
	case strings.Contains(low, "communit") || hasWord(low, "cluster"):
		return "kern_communities", map[string]any{}, true
	case strings.Contains(low, "surprising") || strings.Contains(low, "surprise") || strings.Contains(low, "unexpected connection"):
		return "kern_surprising", map[string]any{}, true
	case strings.Contains(low, "snapshot"):
		return "kern_snapshot", map[string]any{}, true
	case strings.Contains(low, "cycle") || strings.Contains(low, "import graph") || strings.Contains(low, "circular"):
		return "kern_cycles", map[string]any{}, true
	case hasWord(low, "hub") || hasWord(low, "hotspot") || strings.Contains(low, "most depended"):
		return "kern_hubs", map[string]any{}, true
	case hasWord(low, "bridge") || hasWord(low, "coupling"):
		return "kern_bridges", map[string]any{}, true
	case strings.Contains(low, "dead code") || hasWord(low, "unused"):
		return "kern_dead", map[string]any{}, true
	case hasWord(low, "largest") || strings.Contains(low, "god function") || hasWord(low, "biggest"):
		return "kern_larges", map[string]any{}, true
	case strings.Contains(low, "test gap") || hasWord(low, "coverage"):
		return "kern_test_gaps", map[string]any{}, true
	case strings.Contains(low, "entry point") || hasWord(low, "handler") || hasWord(low, "route"):
		return "kern_entry_points", map[string]any{}, true
	case hasWord(low, "framework") || hasWord(low, "library") || strings.Contains(low, "detect stack"):
		return "kern_frameworks", map[string]any{}, true
	case hasWord(low, "churn") || strings.Contains(low, "changed most"):
		return "kern_churn", map[string]any{}, true
	case strings.Contains(low, "cochange") || strings.Contains(low, "co-change") || strings.Contains(low, "lockstep"):
		return "kern_cochange", map[string]any{}, true
	case hasWord(low, "diff") && (strings.Contains(low, "file") || hasWord(low, "compare")):
		return "kern_diff_files", map[string]any{}, true
	case hasWord(low, "review") || strings.Contains(low, "pr "):
		return "kern_review", map[string]any{}, true
	}
	return "", nil, false
}

// classifyGovernanceTools routes the authorized-context case; it must be
// consulted after the architecture cases and before the symbol-level graph
// cases to preserve the original switch's precedence.
func classifyGovernanceTools(low, request string) (string, map[string]any, bool) {
	switch {
	case hasWord(low, "authorize") || hasWord(low, "authorized") ||
		strings.Contains(low, "allowed to see") || strings.Contains(low, "permitted") ||
		strings.Contains(low, "what can i"):
		return "kern_authorize_context", map[string]any{"task": request}, true
	}
	return "", nil, false
}

// classifyGraphTools routes the symbol-level graph/explore cases, extracting a
// symbol from the request when one is present and falling back to kern_search.
func classifyGraphTools(low, request string) (string, map[string]any, bool) {
	switch {
	// Flow questions ("how does the bundle upload flow work end to end?")
	// name a process, not a single symbol: walk the dependency tree from the
	// named symbol when one is extractable, otherwise answer with the
	// system's entry points (handlers/routes) instead of falling through to a
	// flat kern_search list.
	case strings.Contains(low, "flow") || strings.Contains(low, "workflow") || strings.Contains(low, "pipeline") || strings.Contains(low, "end to end") || strings.Contains(low, "end-to-end"):
		tool, args := withSymbol(request, low, "kern_walk", "kern_entry_points", map[string]any{})
		if tool == "kern_walk" {
			args["depth"] = "4"
		}
		return tool, args, true
	case strings.Contains(low, "how does") || hasWord(low, "understand") || hasWord(low, "explain"):
		tool, args := withSymbol(request, low, "kern_explore", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "why does") || strings.Contains(low, "why is") || strings.Contains(low, "rationale"):
		tool, args := withSymbol(request, low, "kern_why", "kern_search", map[string]any{})
		return tool, args, true
	case hasWord(low, "callers") || strings.Contains(low, "who calls") || strings.Contains(low, "call graph"):
		tool, args := withSymbol(request, low, "kern_code_graph", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "inherit") || hasWord(low, "hierarchy") || hasWord(low, "extends") || hasWord(low, "implements"):
		tool, args := withSymbol(request, low, "kern_inherits", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "path from") || strings.Contains(low, "call path") || strings.Contains(low, "shortest path"):
		return "kern_path", map[string]any{}, true
	case hasWord(low, "near") || strings.Contains(low, "depends on") || strings.Contains(low, "neighborhood"):
		tool, args := withSymbol(request, low, "kern_near", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "context for") || strings.Contains(low, "source slice") || strings.Contains(low, "source for"):
		tool, args := withSymbol(request, low, "kern_context", "kern_search", map[string]any{})
		return tool, args, true
	case hasWord(low, "trace") && (strings.Contains(low, "stack") || strings.Contains(low, "pprof")):
		return "kern_trace", map[string]any{}, true
	case hasWord(low, "probe") || strings.Contains(low, "what does this touch"):
		return "kern_probe", map[string]any{"task": request}, true
	case strings.Contains(low, "prose") || strings.Contains(low, "vocab") || strings.Contains(low, "spelling"):
		// prose-word → symbol candidate lookup; kept after the more
		// specific symbol questions so "explain the vocab" still explores.
		return "kern_prose", map[string]any{"query": request}, true
	}
	return "", nil, false
}

// classifyProjectTools routes the project-level utility cases.
func classifyProjectTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "project map") || hasWord(low, "layout") || hasWord(low, "structure") && hasWord(low, "project"):
		return "kern_project_map", map[string]any{}, true
	case strings.Contains(low, "pack") || strings.Contains(low, "bundle"):
		return "kern_pack", map[string]any{}, true
	case strings.Contains(low, "fit context") || strings.Contains(low, "adaptive context") || strings.Contains(low, "compress context") || strings.Contains(low, "fit token"):
		return "kern_fit_context", map[string]any{"query": request}, true
	case hasWord(low, "compact") && strings.Contains(low, "file"):
		return "kern_compact_file", map[string]any{}, true
	// Index intents (dogfood F-2): "rebuild/refresh the index" has no
	// dedicated MCP tool — kern_onboard is the tool that registers and
	// builds/refreshes the index; index status/freshness questions are
	// answered by kern_health (which now falls back to the disk view).
	// Checked before the buddy/onboard branch so "index ... onboard" style
	// phrasings still land on the index intent, and before the health branch
	// so "index status" is unambiguous.
	case strings.Contains(low, "index") && (strings.Contains(low, "rebuild") || strings.Contains(low, "refresh") || hasWord(low, "reindex") || strings.Contains(low, "build the index")):
		return "kern_onboard", map[string]any{}, true
	case strings.Contains(low, "index") && (hasWord(low, "status") || hasWord(low, "fresh") || hasWord(low, "stale") || hasWord(low, "health")):
		return "kern_health", map[string]any{}, true
	// LLM provider intents (dogfood): chain/sampler/status questions are
	// answered by kern_llm_providers ("which local agent should I use");
	// plain code questions ("how does the llm provider work") stay searches.
	case (strings.Contains(low, "llm") && (strings.Contains(low, "providers") || strings.Contains(low, "chain") || strings.Contains(low, "sampler") || strings.Contains(low, "sampling") || hasWord(low, "status"))) ||
		(strings.Contains(low, "host") && (strings.Contains(low, "sampler") || strings.Contains(low, "sampling"))) ||
		(hasWord(low, "agent") && hasWord(low, "use") && (hasWord(low, "local") || hasWord(low, "which"))):
		return "kern_llm_providers", map[string]any{}, true
	case hasWord(low, "buddy") || hasWord(low, "onboard") || strings.Contains(low, "getting started"):
		return "kern_buddy", map[string]any{}, true
	case hasWord(low, "health") || hasWord(low, "status") || strings.Contains(low, "self-check") || strings.Contains(low, "diagnose"):
		return "kern_health", map[string]any{}, true
	case hasWord(low, "stats") || hasWord(low, "savings") || strings.Contains(low, "token count"):
		return "kern_stats", map[string]any{}, true
	case strings.Contains(low, "commit message") || strings.Contains(low, "commitmsg"):
		return "kern_commitmsg", map[string]any{}, true
	case hasWord(low, "memory") || hasWord(low, "remember") || hasWord(low, "lesson"):
		return "kern_memory_recall", map[string]any{"prompt": request}, true
	case hasWord(low, "docs") || hasWord(low, "documentation"):
		return "kern_doc_search", map[string]any{"query": request}, true
	case hasWord(low, "build") || hasWord(low, "test") || hasWord(low, "lint"):
		return "kern_run_build", map[string]any{}, true
	case strings.Contains(low, "repair") || strings.Contains(low, "auto repair") || strings.Contains(low, "fix diagnostics") || strings.Contains(low, "compiler error"):
		return "kern_repair_diagnostics", map[string]any{"compiler_output": request}, true
	case hasWord(low, "exec") || strings.Contains(low, "run script") || strings.Contains(low, "run code"):
		return "kern_exec", map[string]any{}, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := withSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := withSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "rename") || strings.Contains(low, "refactor name"):
		return "kern_rename", map[string]any{}, true
	}
	return "", nil, false
}

// classifyRetrievalTools routes the progressive-disclosure retrieval cases
// (P1/P2/P3 tracker): kern_retrieve, kern_resolve and kern_plan_context. It
// is consulted BEFORE the workflow/arch/graph routers so "plan context",
// "retrieve ..." and "resolve ..." requests beat the broader "plan"/"handler"
// keywords those routers claim. "handle" is matched only as a standalone word
// (never inside "handler", which stays kern_entry_points).
func classifyRetrievalTools(low, request string) (string, map[string]any, bool) {
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
	case (strings.Contains(low, "retrieve") || strings.Contains(low, "handle")) && !hasWord(low, "handler"):
		// NL requests name symbols, not structured handles, so land on the
		// L1 name/token-cost list for the whole request as the query.
		return "kern_retrieve", map[string]any{"query": request, "level": "l1"}, true
	case strings.Contains(low, "context plan") || strings.Contains(low, "plan context") || strings.Contains(low, "explain context") || strings.Contains(low, "planner"):
		return "kern_plan_context", map[string]any{"change": request}, true
	case strings.Contains(low, "orchestrate") || strings.Contains(low, "silent context") || strings.Contains(low, "context pipeline"):
		return "kern_orchestrate", map[string]any{"intent": request}, true
	case strings.Contains(low, "agent skills") || strings.Contains(low, "list skills") || strings.Contains(low, "load skill") || strings.Contains(low, "skill runbook"):
		return "kern_skill", map[string]any{"action": "catalog"}, true
	case strings.Contains(low, "validate note") || (strings.Contains(low, "notes") && strings.Contains(low, "valid")):
		return "kern_note", map[string]any{"action": "validate"}, true
	case (strings.Contains(low, "list") && strings.Contains(low, "notes")) || strings.Contains(low, "note inventory"):
		return "kern_note", map[string]any{"action": "list"}, true
	}
	return "", nil, false
}

// classifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
// classifySkillTools routes explicit skill-language queries (runbook,
// playbook, a literal skill name, or "skill" with load/use/show intent) to
// kern_skill before the workflow router can claim them. Semantic phrases
// WITHOUT skill language deliberately stay un-routed: "make a safe change"
// -> kern_impact and "triage this incident" -> kern_incident are better
// answers than loading the runbook.
func classifySkillTools(low, request string) (string, map[string]any, bool) {
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
	if hasWord(low, "skill") && (strings.Contains(low, "load") || strings.Contains(low, "use") || strings.Contains(low, "show") || strings.Contains(low, "list")) {
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	return "", nil, false
}

func classifyMetaRequest(request string) (string, map[string]any) {
	low := strings.ToLower(request)
	// The sub-routers are consulted in the same order as the original
	// monolithic switch (safety/optimize -> workflows -> architecture ->
	// governance -> symbol graph -> project), so classification outcomes are
	// unchanged; anything unmatched still falls back to kern_search. The
	// retrieval router is consulted FIRST so its specific phrases (plan
	// context, retrieve/resolve) beat the broader workflow/arch keywords.
	if t, a, ok := classifyRetrievalTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifySkillTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyOptimizeTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyWorkflowTools(low, request); ok {
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
	if t, a, ok := classifyArchTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyGovernanceTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyGraphTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyProjectTools(low, request); ok {
		return t, a
	}
	if t, a, _, ok := semanticMetaRoute(request); ok {
		a[viaSemanticArg] = true
		return t, a
	}
	return "kern_search", map[string]any{"query": request}
}
