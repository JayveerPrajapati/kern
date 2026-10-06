// Package metaroute owns the deterministic NL-routing classifier family of
// the kern_meta meta-tool: ClassifyMetaRequest and its sub-routers map a
// natural-language request to the kern_* tool that best answers it via
// deterministic keyword matching, with a dependency-free semantic fallback
// (semantic.go). The package is the classifier half of the split: it must
// NOT import internal/mcp/meta (the Handle/Hooks dispatch surface imports
// this package, never the other way), so the whole router chain — including
// the explicit-name arm, the memory-add arm and the ExtractSymbol/
// ExtractAfterColon helpers the sub-routers lean on — lives here.
package metaroute

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/skills"
)

// WorkingsetArg marks the kern_meta workingset route: ClassifyMetaRequest
// answers "working set" / "workingset" requests as kern_context with this
// boolean argument set, Handle answers the marker through the Workingset
// hook instead of dispatching, and the mcp root's cache gate
// (cacheableForCall) uses the same marker to keep the route out of the D1
// cache (B1, ADR-0012). The value is deliberately a boolean: it can never be
// a legitimately requested kern_context argument.
const WorkingsetArg = "workingset"

// WorkingsetIntent reports whether a lowercase kern_meta request asks for
// the caller's conditional-fetch working-set registry. Only possessive,
// imperative or single-token command spellings fire: "my working set",
// "show working set", "show my working set" (covered by the possessive
// form), the joined "workingset" and the hyphenated "working-set". A bare
// two-word substring inside a larger question — "how does the working set
// registry work" — must NOT be hijacked into the registry listing
// (MEDIUM-5); it keeps its normal routing.
func WorkingsetIntent(low string) bool {
	if strings.Contains(low, "workingset") || strings.Contains(low, "working-set") {
		return true
	}
	return strings.Contains(low, "my working set") || strings.Contains(low, "show working set")
}

// explicitToolNameRe matches a literal kern_<name> catalog tool token as a
// standalone word, case-insensitively ("kern_rename", "KERN_SEARCH"). Word
// boundaries keep "kern_searching" (a prose word) and "kern_search_limits"
// (a longer identifier) from hijacking the name; the alternation is sorted
// longest-first so a tool name that is a prefix of another (none today, but
// the catalog can grow) still matches the longer name at a shared start
// position. kern_meta is excluded: routing to the router itself would
// recurse, mirroring its exclusion from the meta dispatch table.
var explicitToolNameRe = func() *regexp.Regexp {
	names := make([]string, 0, len(catalog.All))
	for _, tool := range catalog.All {
		if tool.Name == "kern_meta" {
			continue
		}
		names = append(names, regexp.QuoteMeta(tool.Name))
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(names, "|") + `)\b`)
}()

// classifyExplicitToolName returns the catalog tool a request names
// literally ("use kern_rename", "call kern_sandbox", "run kern_loop"), or
// ok=false when no literal kern_<name> token is present. This is the
// name-addressed arm of the router: every catalog tool is reachable this
// way even when no keyword arm exists, while requests WITHOUT a literal
// kern_* name keep the keyword routing below unchanged. The matched token
// is lowercased so "KERN_SEARCH" normalizes to the catalog name.
func classifyExplicitToolName(request string) (string, map[string]any, bool) {
	if m := explicitToolNameRe.FindString(request); m != "" {
		return strings.ToLower(m), map[string]any{}, true
	}
	return "", nil, false
}

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

// RequiredParamName extracts the parameter name from a missing-param error
// of the exact shape "<identifier> is required" — the form kern tool
// handlers generate. Prose prefixes ("a license file is required") and
// non-identifier tokens do not match, so the passthrough hint is only
// taught when it names a real parameter.
func RequiredParamName(msg string) (string, bool) {
	i := strings.Index(msg, " is required")
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(msg[:i]))
	if len(fields) != 1 {
		return "", false
	}
	name := fields[0]
	for _, r := range name {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", false
		}
	}
	return name, true
}

// howWhyRe captures the bare symbol in "how does X work?" style questions:
// the first word after the how-does/how-do/why-does phrase, optionally
// preceded by an article ("how does the index work" -> "index"). Lowercase
// start only — CamelCase/dotted symbols are already handled by ExtractSymbol.
var howWhyRe = regexp.MustCompile(`\b(?:how does|how do|why does)\s+(?:(?:the|a|an)\s+)?([a-z][a-z0-9_.]*)`)

// HowWhySymbol extracts the bare lowercase symbol from "how does X work?"
// style questions, which ExtractSymbol deliberately misses (it only pulls
// quoted/dotted/CamelCase tokens). The generic template words
// ("work", "function", "behave", "works") are not symbols — a question like
// "how does work happen?" must not invent a "work" symbol. Returns "" when
// there is no plausible symbol, so the caller falls back to kern_search.
func HowWhySymbol(low string) string {
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

// contextForRe captures the bare symbol in "show context for X" style
// requests: the first token after the context/source phrase, optionally
// preceded by an article ("context for the index" -> "index"). Lowercase
// start only — CamelCase/dotted symbols are already handled by
// ExtractSymbol.
var contextForRe = regexp.MustCompile(`\b(?:context for|source for|source slice(?:\s+for)?)\s+(?:(?:the|a|an)\s+)?([a-z][a-z0-9_.]*)`)

// contextForSymbol extracts the bare lowercase symbol from "context for X"
// / "source for X" requests, which ExtractSymbol deliberately misses (it
// only pulls quoted/dotted/CamelCase tokens). Without it the context arm
// fell through to kern_search for bare symbols ("show context for
// dispatch") even though the intent was a source slice. Returns "" when
// there is no plausible symbol, so the caller keeps the search fallback.
func contextForSymbol(low string) string {
	m := contextForRe.FindStringSubmatch(low)
	if m == nil {
		return ""
	}
	switch m[1] {
	case "work", "function", "behave", "works", "this", "that", "it", "the":
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
		return "kern_optimize", map[string]any{"action": "log", "log": ExtractAfterColon(request, low)}, true
	case HasWord(low, "compress") && (HasWord(low, "output") || HasWord(low, "response") || HasWord(low, "reply")):
		return "kern_optimize", map[string]any{"action": "output", "text": ExtractAfterColon(request, low)}, true
	case HasWord(low, "compress") && HasWord(low, "prompt"):
		return "kern_optimize", map[string]any{"action": "prompt", "prompt": ExtractAfterColon(request, low)}, true
	case HasWord(low, "security") || (HasWord(low, "scan") && strings.Contains(low, "vulnerab")) || HasWord(low, "cve"):
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
	case strings.Contains(low, "what breaks") || strings.Contains(low, "what happens") || strings.Contains(low, "what would happen") || HasWord(low, "impact") || (HasWord(low, "change") && !HasWord(low, "analyze")):
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
		return "kern_memory", map[string]any{"action": "ranked", "prompt": request}, true
	case strings.Contains(low, "policy dsl") || strings.Contains(low, "evaluate policy"):
		return "kern_policy_dsl", map[string]any{}, true
	case strings.Contains(low, "agent coordination") || (HasWord(low, "coordination") && strings.Contains(low, "agent")):
		return "kern_agent", map[string]any{"action": "coordination", "inner_action": "status"}, true
	case strings.Contains(low, "rbac") || strings.Contains(low, "agent role"):
		return "kern_agent", map[string]any{"action": "rbac", "inner_action": "roles"}, true
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
	// cycle/import-graph/circular and hotspot/bottleneck/fragile requests are
	// routed by ClassifyStructureTools (with symbol deferral) before the
	// workflow router; they are intentionally NOT re-matched here, so symbol
	// questions like "how does ImportCycles work" reach the graph router.
	case HasWord(low, "hub") || strings.Contains(low, "most depended"):
		return "kern_hubs", map[string]any{}, true
	case HasWord(low, "bridge") || HasWord(low, "coupling"):
		return "kern_bridges", map[string]any{}, true
	case strings.Contains(low, "dead code") || HasWord(low, "unused"):
		return "kern_dead", map[string]any{}, true
	case HasWord(low, "largest") || strings.Contains(low, "god function") || HasWord(low, "biggest"):
		return "kern_larges", map[string]any{}, true
	case strings.Contains(low, "test gap") || HasWord(low, "coverage") || HasWord(low, "untested"):
		return "kern_test_gaps", map[string]any{}, true
	case strings.Contains(low, "entry point") || HasWord(low, "handler") || HasWord(low, "route"):
		return "kern_entry_points", map[string]any{}, true
	case HasWord(low, "framework") || HasWord(low, "library") || strings.Contains(low, "detect stack"):
		return "kern_frameworks", map[string]any{}, true
	case HasWord(low, "churn") || strings.Contains(low, "changed most") || strings.Contains(low, "most changed"):
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
		if sym := HowWhySymbol(low); sym != "" {
			return "kern_explore", map[string]any{"symbol": sym}, true
		}
		tool, args := WithSymbol(request, low, "kern_explore", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "why does") || strings.Contains(low, "why is") || strings.Contains(low, "why do ") || strings.Contains(low, "rationale"):
		if sym := ExtractSymbol(request, low); sym != "" {
			return "kern_why", map[string]any{"symbol": sym}, true
		}
		if sym := HowWhySymbol(low); sym != "" {
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
		// Bare lowercase symbols ("show context for dispatch") are missed by
		// ExtractSymbol; without this they fell through to kern_search even
		// though the intent was a source slice. Extract the phrase-following
		// token so the request reaches kern_context (F3).
		if sym := ExtractSymbol(request, low); sym != "" {
			return "kern_context", map[string]any{"symbol": sym}, true
		}
		if sym := contextForSymbol(low); sym != "" {
			return "kern_context", map[string]any{"symbol": sym}, true
		}
		tool, args := WithSymbol(request, low, "kern_context", "kern_search", map[string]any{})
		return tool, args, true
	case HasWord(low, "trace") && (strings.Contains(low, "stack") || strings.Contains(low, "pprof")):
		return "kern_trace", map[string]any{}, true
	case HasWord(low, "probe") || strings.Contains(low, "what does this touch") || strings.Contains(low, "blast radius"):
		return "kern_probe", map[string]any{"task": request}, true
	case strings.Contains(low, "prose") || strings.Contains(low, "vocab") || strings.Contains(low, "spelling"):
		// prose-word → symbol candidate lookup; kept after the more
		// specific symbol questions so "explain the vocab" still explores.
		return "kern_prose", map[string]any{"query": request}, true
	}
	return "", nil, false
}

// compactFileArgs extracts the file path from a "compact/compress this
// file: X" request: the text after the first colon, trimmed
// (ExtractAfterColon, the same helper the log/mask/compress-type tools
// use). A request with no colon yields empty args — the kern_compact_file
// handler then answers with its own "path is required" guidance instead of
// the router inventing a path or misrouting to another tool.
func compactFileArgs(request, low string) map[string]any {
	if i := strings.Index(low, ":"); i >= 0 {
		if p := ExtractAfterColon(request, low); p != "" {
			return map[string]any{"path": p}
		}
	}
	return map[string]any{}
}

// classifyFileTools routes the file-shaped intents (fit-context budget,
// compact/compress-this-file, summarize-this-file). It is consulted BEFORE
// the workflow/architecture/governance/graph keyword routers: the file PATH
// in these requests often contains the very substrings those arms match on
// (workflow.go hits the flow arm, pipeline/handler.go hits the handler arm),
// and the file intent is always the more specific reading
// (specific-before-generic). Relative precedence inside the family is
// unchanged: the optimize compress arms (log/output/prompt) run earlier in
// the chain, fit-context outranks compact-file ("compress context of this
// file"), and summarize-file keeps routing with empty args.
func classifyFileTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "fit context") || strings.Contains(low, "adaptive context") || strings.Contains(low, "compress context") || strings.Contains(low, "fit token"):
		return "kern_fit_context", map[string]any{"query": request}, true
	case (HasWord(low, "compact") || HasWord(low, "compress")) && strings.Contains(low, "file"):
		// "compact/compress this file: X": the path follows the colon
		// (compactFileArgs via ExtractAfterColon). A no-path request still
		// routes — the handler's own "path is required" guidance speaks.
		return "kern_compact_file", compactFileArgs(request, low), true
	case (HasWord(low, "summarize") || HasWord(low, "summary") || HasWord(low, "overview")) && strings.Contains(low, "file"):
		// "summarize this file X" is a compact_file intent, not a search.
		return "kern_compact_file", map[string]any{}, true
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
	// Index intents: "rebuild/refresh the index" has no
	// dedicated MCP tool — kern_onboard is the tool that registers and
	// builds/refreshes the index; index status/freshness questions are
	// answered by kern_health (which falls back to the disk view).
	// Checked before the buddy/onboard branch so "index ... onboard" style
	// phrasings still land on the index intent, and before the health branch
	// so "index status" is unambiguous.
	case strings.Contains(low, "index") && (strings.Contains(low, "rebuild") || strings.Contains(low, "refresh") || HasWord(low, "reindex") || strings.Contains(low, "build the index")):
		return "kern_onboard", map[string]any{}, true
	case strings.Contains(low, "index") && (HasWord(low, "status") || HasWord(low, "fresh") || HasWord(low, "stale") || HasWord(low, "health")):
		return "kern_health", map[string]any{}, true
	// LLM provider intents: chain/sampler/status questions are
	// answered by kern_llm_providers ("which local agent should I use");
	// plain code questions ("how does the llm provider work") stay searches.
	case (strings.Contains(low, "llm") && (strings.Contains(low, "providers") || strings.Contains(low, "chain") || strings.Contains(low, "sampler") || strings.Contains(low, "sampling") || HasWord(low, "status"))) ||
		(strings.Contains(low, "host") && (strings.Contains(low, "sampler") || strings.Contains(low, "sampling"))) ||
		(HasWord(low, "agent") && HasWord(low, "use") && (HasWord(low, "local") || HasWord(low, "which"))):
		return "kern_llm_providers", map[string]any{}, true
	case HasWord(low, "buddy") || HasWord(low, "onboard") || HasWord(low, "onboarding") || strings.Contains(low, "getting started"):
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
	case HasWord(low, "health") || HasWord(low, "doctor") || HasWord(low, "status") || strings.Contains(low, "self-check") || strings.Contains(low, "diagnose"):
		return "kern_health", map[string]any{}, true
	case HasWord(low, "stats") || HasWord(low, "savings") || HasWord(low, "saved") || strings.Contains(low, "token count") || strings.Contains(low, "token usage"):
		return "kern_stats", map[string]any{}, true
	case strings.Contains(low, "commit message") || strings.Contains(low, "commitmsg") || strings.Contains(low, "commit msg"):
		return "kern_commitmsg", map[string]any{}, true
	case HasWord(low, "memory") || HasWord(low, "memories") || HasWord(low, "remember") || HasWord(low, "lesson") || HasWord(low, "lessons") || HasWord(low, "learnings"):
		return "kern_memory", map[string]any{"action": "recall", "prompt": request}, true
	case HasWord(low, "doc") || HasWord(low, "docs") || HasWord(low, "documentation"):
		return "kern_doc", map[string]any{"action": "search", "query": request}, true
	// Synthesize-test intents (router audit): "write/generate/scaffold a
	// test" name test GENERATION, not test RUNNING — they must beat the
	// generic build/test/lint arm below, which would otherwise answer a
	// generation request by merely re-running the suite.
	case strings.Contains(low, "synthesize") || strings.Contains(low, "generate a test") || strings.Contains(low, "write a test") || strings.Contains(low, "scaffold a test") || strings.Contains(low, "test skeleton"):
		return "kern_synthesize_test", synthesizeArgs(request), true
	// Heal intents (router audit): auto-repair phrasings route to the heal
	// loop — the arm answers "make it work" — before the repair-diagnostics
	// arm ("what is wrong") and before build/test/lint (which only re-runs
	// the check that already failed).
	case HasWord(low, "heal") || strings.Contains(low, "failing build") || strings.Contains(low, "failing test") || strings.Contains(low, "auto-repair") || strings.Contains(low, "fix the build") || strings.Contains(low, "fix the tests"):
		return "kern_heal", map[string]any{}, true
	// Repair-diagnostics intents (router audit): the original arm matched
	// "compiler error" literally — the common variants ("compile error",
	// "compilation error", "build error") fell through to build/test/lint.
	// Ordered before the generic arm for the same reason as heal.
	case strings.Contains(low, "repair") || strings.Contains(low, "fix diagnostics") || strings.Contains(low, "compiler error") || strings.Contains(low, "compile error") || strings.Contains(low, "compilation error") || strings.Contains(low, "build error"):
		return "kern_repair", map[string]any{"action": "diagnostics", "compiler_output": request}, true
	case HasWord(low, "build") || HasWord(low, "test") || HasWord(low, "lint") || HasWord(low, "validate"):
		return "kern_validate", map[string]any{}, true
	case HasWord(low, "exec") || HasWord(low, "execute") || strings.Contains(low, "run script") || strings.Contains(low, "run code") || strings.Contains(low, "run the script") || strings.Contains(low, "run this script"):
		return "kern_exec", map[string]any{}, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := WithSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "rename") || strings.Contains(low, "refactor name"):
		return "kern_rename", map[string]any{}, true
	}
	return "", nil, false
}

// synthesizeArgs extracts a target symbol and/or source file from a
// "write a test for X in Y.ext" request. The synthesize-test handler needs
// at least one of them (target or file) — the router arm's job is to find
// both when the request names them. The scan uses the ORIGINAL request
// (not the lowercased copy) because symbol casing is the signal: a
// capitalized token is a symbol candidate, a code-extension token is a
// file. Punctuation-trimmed, dotted tokens are treated as files only.
func synthesizeArgs(request string) map[string]any {
	args := map[string]any{}
	for _, word := range strings.Fields(request) {
		w := strings.Trim(word, ".,;:!?()[]{}\"`'")
		if w == "" {
			continue
		}
		lw := strings.ToLower(w)
		switch {
		case args["file"] == nil &&
			(strings.HasSuffix(lw, ".go") || strings.HasSuffix(lw, ".py") || strings.HasSuffix(lw, ".ts") ||
				strings.HasSuffix(lw, ".js") || strings.HasSuffix(lw, ".java") || strings.HasSuffix(lw, ".rb")):
			args["file"] = w
		case args["target"] == nil && !strings.Contains(w, "."):
			hasUpper := false
			for _, r := range w {
				if r >= 'A' && r <= 'Z' {
					hasUpper = true
					break
				}
			}
			if hasUpper && len(w) > 2 {
				args["target"] = w
			}
		}
	}
	return args
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
	case strings.Contains(low, "run this task") || strings.Contains(low, "run the task") || strings.Contains(low, "run this workflow") || (HasWord(low, "run") && HasWord(low, "autonomous")):
		// kern_run is a default-22 tool. Route task-execution phrasings to the pipeline runner.
		return "kern_run", map[string]any{"intent": request}, true
	case strings.Contains(low, "agent skills") || strings.Contains(low, "list skills") || strings.Contains(low, "load skill") || strings.Contains(low, "skill runbook"):
		return "kern_skill", map[string]any{"action": "catalog"}, true
	}
	return "", nil, false
}

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

// Flow-router phrases. Word-boundary matched so a substring inside another
// word never fires: "workflow of X" has no word boundary before "flow", so
// it keeps its workflow routes; "profiles" never matches "flow". The
// connect arm requires "connect" AND an "end to end"/"end-to-end" marker —
// a bare "end to end" question ("how does the bundle upload flow work end
// to end?") keeps its existing kern_entry_points/kern_near routing (pinned
// by TestClassifyMetaRequest_Flow), while "how do A and B connect" fires
// on the how-do-connect phrase alone. "flows? through" and "flows? from"
// cover the transitive phrasings ("how does X flow through Y", "how does
// the request flow from login to checkout") that previously fell through
// to the graph arm's entry-points fallback.
var flowPhraseRe = regexp.MustCompile(
	`\btrace the flow\b|\bflow of\b|\bfollow the flow\b|` +
		`\bflow(?:s)? (?:from|through)\b|` +
		`\bconnect(?:ed|ing)?\b.*\bend[ -]to[ -]end\b|` +
		`\bhow do(?:es)?\s+.+?\s+and\s+.+?\s+connect\b`,
)

// Two-symbol extraction patterns for flow questions. Each captures exactly
// one token per side (articles skipped, trailing phrase words skipped), so
// kern_path receives symbol-shaped from/to values, never whole phrases:
// "how do authentication and session handling connect end to end" yields
// from=authentication, to=session. They run on the ORIGINAL request
// case-insensitively and capture the ORIGINAL casing: intel.Resolve matches
// symbol names exactly, so "how do handleCycles and handlePath connect end
// to end" must yield from=handleCycles, to=handlePath — lowercasing the
// capture would make the resolved path fail.
var (
	flowBetweenRe = regexp.MustCompile(`(?i)\bbetween\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\s+and\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\b`)
	flowFromToRe  = regexp.MustCompile(`(?i)\bfrom\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\s+to\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\b`)
	flowConnectRe = regexp.MustCompile(`(?i)\bhow do(?:es)?\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\s+and\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)(?:\s+[a-zA-Z0-9_.]+)*\s+connect\b`)
	flowOfRe      = regexp.MustCompile(`(?i)\b(?:trace the flow of|follow the flow of|flow of)\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)`)
	flowThroughRe = regexp.MustCompile(`(?i)\bthrough\s+(?:(?:the|a|an)\s+)?([a-zA-Z0-9_.]+)`)
)

// ClassifyFlowTools routes end-to-end flow questions — "trace the flow of
// dispatch through the system", "follow the flow from login to checkout",
// "how do authentication and session handling connect end to end", "trace
// how data flows through dispatch" — to the call-chain tools: kern_path
// when the request names two symbols (between/from-to/"X and Y connect"),
// kern_explore when it names one (explore includes call flow), and
// kern_explain for a bare flow question with no symbol at all. The trigger
// runs on the lowercased request; the symbol extraction runs on the
// ORIGINAL request so captured from/to keep their casing (intel.Resolve is
// case-sensitive). Route-path questions are NOT flow questions: a request
// carrying an HTTP route (extractRoute) belongs to the structure arm, which
// searches for the route itself ("trace the flow of requests to /v1/loop"
// → kern_search "/v1/loop"). It runs BEFORE the retrieval/structure/
// workflow/arch routers so the retrieval arm's sloppy "handle" substring
// and the graph arm's entry-points fallback cannot capture a flow question
// (live misroutes: "trace the flow of dispatch through the system", "how do
// authentication and session handling connect end to end", and "how do
// handleCycles and handlePath connect end to end" all landed on the wrong
// tool).
func ClassifyFlowTools(low, request string) (string, map[string]any, bool) {
	if !flowPhraseRe.MatchString(low) {
		return "", nil, false
	}
	// Route-path questions belong to the structure arm (route search), not
	// the flow arm — consulted before any capture so "flow of requests to
	// /v1/loop" never degrades to a bogus symbol ("requests").
	if extractRoute(low) != "" {
		return "", nil, false
	}
	// Two symbols named → shortest call path between them. Extraction runs
	// on the original request to preserve symbol casing.
	if m := flowBetweenRe.FindStringSubmatch(request); m != nil {
		return "kern_path", map[string]any{"from": m[1], "to": m[2]}, true
	}
	if m := flowFromToRe.FindStringSubmatch(request); m != nil {
		return "kern_path", map[string]any{"from": m[1], "to": m[2]}, true
	}
	if m := flowConnectRe.FindStringSubmatch(request); m != nil {
		return "kern_path", map[string]any{"from": m[1], "to": m[2]}, true
	}
	// One symbol → explore (its call flow is the answer). flowOfRe runs
	// before flowThroughRe so "trace the flow of dispatch through the
	// system" yields "dispatch", not "system".
	if sym := ExtractSymbol(request, low); sym != "" {
		return "kern_explore", map[string]any{"symbol": sym}, true
	}
	if m := flowOfRe.FindStringSubmatch(request); m != nil {
		return "kern_explore", map[string]any{"symbol": m[1]}, true
	}
	if m := flowThroughRe.FindStringSubmatch(request); m != nil {
		return "kern_explore", map[string]any{"symbol": m[1]}, true
	}
	// No symbol → end-to-end architectural narrative for the request.
	return "kern_explain", map[string]any{"target": request}, true
}

// SearchLocatorIntent reports whether a lowercase request is a plain symbol
// locator ("find/search/lookup/locate X", "where is X") — the search
// fallback's documented legitimate use, which router_test.go pins ("the
// default search fallback for plain locate requests"). Handle consults it
// so locator requests keep dispatching kern_search instead of being
// intercepted by the low-confidence candidates branch.
func SearchLocatorIntent(low string) bool {
	return HasWord(low, "find") || HasWord(low, "search") || HasWord(low, "lookup") ||
		HasWord(low, "locate") || strings.Contains(low, "where is")
}

// ClassifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
func ClassifyMetaRequest(request string) (string, map[string]any) {
	low := strings.ToLower(request)
	// Explicit tool-name intents (catalog coverage): a request that names a
	// kern_* catalog tool literally ("use kern_rename", "call kern_sandbox",
	// "run kern_loop") routes directly to that tool — every catalog tool is
	// reachable this way even when no keyword arm exists. This is the
	// name-addressed arm the plugin's "NL router → all sub-tools" promise
	// leans on; requests WITHOUT a literal kern_* name keep the keyword
	// routing below unchanged.
	if t, a, ok := classifyMemoryAdd(request); ok {
		return t, a
	}
	if t, a, ok := classifyExplicitToolName(request); ok {
		return t, a
	}
	// B1 workingset route (ADR-0012): "my working set" / "workingset"
	// requests answer with the caller's conditional-fetch registry. Routed to
	// kern_context with the marker argument set; Handle answers the marker
	// route through the Workingset hook instead of dispatching, and the D1
	// cache gate (cacheableForCall) excludes it from caching.
	if WorkingsetIntent(low) {
		return "kern_context", map[string]any{"symbol": request, WorkingsetArg: true}
	}
	// End-to-end flow questions ("trace the flow of X", "how do A and B
	// connect end to end") are claimed BEFORE the retrieval router: the
	// retrieval arm deliberately substring-matches "handle" for
	// "resolve handle <id>"-style requests, which would otherwise steal a
	// flow question naming handle* symbols ("how do handleCycles and
	// handlePath connect end to end" → kern_retrieve). The flow phrases
	// share no words with the retrieval phrases, so the pinned retrieval
	// routes are untouched. Consulted before structure/workflow/arch too,
	// so the graph arm's entry-points fallback cannot capture a flow
	// question as a flat listing (live misroutes #1/#3). Word-boundary
	// phrases only: "workflow of X" and bare "end to end" without
	// "connect" keep their existing routes.
	if t, a, ok := ClassifyFlowTools(low, request); ok {
		return t, a
	}
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
	// File-shaped intents (fit-context, compact/compress/summarize-file) are
	// consulted before the generic keyword routers: their file PATH often
	// contains keyword substrings ("workflow.go", "pipeline/handler.go")
	// that would otherwise hijack the request (live regression found by the
	// closeout probe: "compress this file: internal/agent/workflow.go"
	// misrouted to kern_near with the path as the symbol).
	if t, a, ok := classifyFileTools(low, request); ok {
		return t, a
	}
	// Structure questions (import cycles, layering violations, hotspots) are
	// routed BEFORE the workflow router so its generic "analyze"/"plan"/
	// "change" keywords cannot claim them; the structure router itself defers
	// to impact/what-if/symbol questions, so "what breaks if I change the
	// cycle detector" and "how does ImportCycles work" keep their routes.
	if t, a, ok := ClassifyStructureTools(low, request); ok {
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
	// Final fallback: kern_search with the raw request. The via_fallback
	// marker lets Handle answer low-confidence code-intent requests with a
	// ranked-candidates shortlist instead of running a search; the tool name
	// stays kern_search so every existing call site and classifier test is
	// unaffected.
	return "kern_search", map[string]any{"query": request, ViaFallbackArg: true}
}
