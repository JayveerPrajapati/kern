// Package meta owns the kern_meta dispatch surface: Handle implements the
// kern_meta tool body and Hooks wires it to the owning MCP server. The
// deterministic NL classifier (ClassifyMetaRequest and its sub-routers) and
// the dependency-free semantic fallback moved to internal/metaroute —
// meta imports metaroute where Handle calls the router, never the other way.
// meta keeps the dispatch-side machinery: the catalog-derived routable set,
// the explicit tool-name arm the Handle tests lean on is in metaroute, and
// this package retains the refusal gate (NoCodeIntentError), the code-intent
// family and the ExtractSymbol/ExtractAfterColon wrappers the Handle surface
// and its tests use.
package meta

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/metaroute"
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
	// CostHint returns the deterministic out-token estimate for a tool
	// (adapter delegates to the server's costHintFor). Handle surfaces the
	// token estimate next to the MEASURED wall-clock latency — the static
	// estMs is no longer printed (it was presented as a latency promise).
	// nil omits the cost line from the classified output.
	CostHint func(tool string) (estMs, estTokens int)
	// ToolCatalog renders the server's registered tool table (one line per
	// tool: name, phase, risk, one-line description). When non-nil, Handle
	// answers tool-catalog requests ("give me the full tool catalog") with
	// the rendered table instead of falling through to classification and
	// the symbol-search dead end. nil preserves the legacy behavior.
	ToolCatalog func() (string, error)
	// Workingset renders the caller's conditional-fetch working-set registry
	// (the etags this agent has been served, per tool/args, newest first) for
	// "my working set" / "workingset" kern_meta requests (B1, ADR-0012). When
	// non-nil, Handle answers the classified workingset route with it instead
	// of dispatching the marker-routed tool. nil preserves the legacy
	// behavior.
	Workingset func(agentID string) (string, error)
}

// metaRoutedTools is the set of tool names the kern_meta router can dispatch
// to. It is derived from the catalog (internal/mcp/catalog.All — the single
// source of truth for tool registration, which the architecture ledger
// already allows this package to import) so every catalog tool except
// kern_meta itself is routable: routing to the router would recurse, so it
// is deliberately excluded. Deriving the set instead of hand-maintaining a
// 76-name map keeps the drift gate (TestMetaRouterCatalogCoverage) and the
// plugin's "NL router → all sub-tools" promise honest as the catalog grows.
// The set is built once at package init; it is not sorted (Handle only
// membership-tests it, and RoutableTools sorts on return). Names the
// classifier produces that are NOT in this set — a classified name that is
// not a catalog tool — still fall back to kern_search with the raw request
// as the query: Handle must preserve that behavior byte-for-byte.
var metaRoutedTools = func() map[string]bool {
	names := make(map[string]bool, len(catalog.All))
	for _, tool := range catalog.All {
		if tool.Name == "kern_meta" {
			continue // routing to the router itself would recurse
		}
		names[tool.Name] = true
	}
	return names
}()

// RoutableTools returns the sorted set of tool names the kern_meta router
// can dispatch to (metaRoutedTools). This is exactly the set of names Handle
// will ever call the RouteTool hook with: names the classifier produces that
// are outside this set are rewritten to the kern_search fallback before
// dispatch. Exposed so callers (e.g. the router-coverage drift gate in
// internal/mcp) can compare router reach against the catalog without
// duplicating the table.
func RoutableTools() []string {
	names := make([]string, 0, len(metaRoutedTools))
	for name := range metaRoutedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// isWorkingsetRoute reports whether the classification produced the
// workingset marker route. Handle consults it right after classification —
// before the dispatch/fallback logic — so the Workingset hook renders the
// registry and the kernel_context handler never runs.
func isWorkingsetRoute(tool string, subArgs map[string]any) bool {
	return tool == "kern_context" && mcpargs.ArgBool(subArgs, metaroute.WorkingsetArg)
}

// NoCodeIntentError is returned by Handle when an unrecognized request falls
// through to the search fallback but names no code, repo, or kern-tooling
// vocabulary (e.g. "make me a sandwich"). The classifier's fallback is the
// signal that no explore/impact/plan/etc. branch claimed the request; running
// a symbol search on such a request would return confident-wrong code, so
// Handle refuses with guidance instead. The CLI maps this to a usage-style
// exit (2); MCP surfaces it as a tool error so agents learn to call
// kern search / kern arch / kern buddy explicitly instead of trusting a junk
// result.
type NoCodeIntentError struct {
	Request string
}

func (e *NoCodeIntentError) Error() string {
	return fmt.Sprintf("no code intent detected in request %q — try: kern search <symbol> | kern arch | kern buddy", clipEcho(e.Request))
}

// clipEcho truncates a request echoed in an output header or error message
// to 80 chars, keeping the full text only when it fits. A 30x-repeated query
// must not blow up the classified line, a level header, or a refusal message.
func clipEcho(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "…"
}

// codeIntentWords are vocabulary signals that a request is about code, the
// repo, or the kern toolchain — the legitimate uses of the search fallback.
// A fallback request matching none of them (no symbol, no code word) is not
// a code question, and Handle refuses it instead of running a junk search.
// Kept deliberately code-specific: generic phrases junk questions lean on
// ("what is", "show me", "where is") are NOT signals — the protected locator
// phrasings ("find the NewServer function") carry their own symbol or
// code-word signals ("find", "function", CamelCase symbol).
var codeIntentWords = []string{
	// code & symbols
	"code", "symbol", "function", "method", "struct", "class", "interface",
	"variable", "constant", "field", "parameter", "argument", "return",
	"type", "package", "module", "import", "export", "api", "endpoint",
	"handler", "route", "router", "dispatch", "server", "client", "request",
	"middleware", "plugin", "library", "framework", "binary", "compiler",
	"compile", "build", "test", "lint", "bug", "error", "panic", "stack",
	"trace", "debug", "refactor", "rename", "delete", "remove", "change",
	"impact", "break", "call", "caller", "callee", "depend", "dependency",
	"graph", "index", "search", "find", "lookup", "query", "file", "repo",
	"repository", "directory", "config", "docs", "documentation", "deploy",
	"pipeline", "workflow", "runner", "schema", "cache", "thread", "process",
	"memory", "profile", "perf", "benchmark", "commit", "branch", "merge",
	"release", "version", "issue", "coverage", "security", "vuln",
	// kern tooling
	"tool", "catalog", "skill", "usage", "guide", "cli", "command",
	"subcommand", "status", "health",
}

// codeIntentWordsRe matches any code-intent word as a standalone word with an
// optional plural/tense suffix, so "functions", "changes", "testing" and
// "indexed" count as their base word while "protest"/"latest" never match
// "test" (word-boundary, no blind substring).
var codeIntentWordsRe = func() *regexp.Regexp {
	parts := make([]string, len(codeIntentWords))
	for i, w := range codeIntentWords {
		parts[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(parts, "|") + `)(?:s|es|ed|ing)?\b`)
}()

// codeIntent reports whether a search-fallback request names code, the repo,
// or kern tooling: a quoted/dotted/CamelCase symbol (ExtractSymbol), a
// "how does X work" symbol (metaroute.HowWhySymbol), or any code-intent
// vocabulary word. The refusal gate applies ONLY on the search-fallback path
// — requests the classifier routed to explore/impact/plan/... never consult
// it.
func codeIntent(request, low string) bool {
	if ExtractSymbol(request, low) != "" {
		return true
	}
	if metaroute.HowWhySymbol(low) != "" {
		return true
	}
	return codeIntentWordsRe.MatchString(low)
}

// textAfterColon returns the payload after the first ':' of a request such as
// "mask secrets in: token=abc" (empty when there is no colon or no payload),
// so inline-payload examples work without the args passthrough.
func textAfterColon(request string) string {
	_, after, ok := strings.Cut(request, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(after)
}

// Handle implements the kern_meta tool body: it takes a natural-language
// request, classifies it via metaroute.ClassifyMetaRequest, dispatches to
// the chosen tool through h.RouteTool, and returns the result prefixed with
// the classification. It mirrors the legacy *Server.handleMeta exactly: only
// the tools in metaRoutedTools are dispatched; any other name — including a
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
	start := time.Now()
	tool, subArgs := metaroute.ClassifyMetaRequest(request)
	viaSemantic := false
	if v, _ := subArgs[metaroute.ViaSemanticArg].(bool); v {
		delete(subArgs, metaroute.ViaSemanticArg)
		viaSemantic = true
	}
	if root != "" {
		subArgs["root"] = root
	}
	if tool == "kern_mask_pii" && mcpargs.ArgString(subArgs, "text") == "" {
		if text := textAfterColon(request); text != "" {
			subArgs["text"] = text
		}
	}
	// Forward the governed-mode agent context (P1.2) so kern_meta's routed
	// retrieval sub-tools can authorize: agent_id/task/scope reach the same
	// handlers an explicit kern_explore/kern_context/kern_graph call would.
	for _, k := range []string{"agent_id", "task", "scope"} {
		if v, ok := args[k]; ok {
			subArgs[k] = v
		}
	}
	// Structured passthrough (F3): the caller can supply an optional `args`
	// object whose entries are merged into the routed tool's params AFTER
	// classification, so params the request text cannot carry (path,
	// pattern, command, ...) reach the routed handler. Explicit passthrough
	// values override classifier-synthesized ones on key collision — the
	// caller's structured value is the more authoritative signal.
	if passthrough, ok := args["args"].(map[string]any); ok {
		for k, v := range passthrough {
			subArgs[k] = v
		}
	}
	// B1 workingset route (ADR-0012): a request classified to the workingset
	// marker answers with the caller's conditional-fetch registry (tool, args
	// digest, etag, timestamp) through the Workingset hook — never by
	// dispatching the marker-routed kern_context. The route is non-cacheable
	// (the mcp root's cache gate excludes the marker), so the listing is
	// always current per-agent state.
	if isWorkingsetRoute(tool, subArgs) {
		if h.Workingset == nil {
			return "", fmt.Errorf("meta: workingset hook is not wired")
		}
		return h.Workingset(mcpargs.ArgString(args, "agent_id"))
	}
	// Non-code refusal (P1): the search fallback is only legitimate when the
	// request names code, the repo, or kern tooling. When the classifier fell
	// through to kern_search (its final fallback for unrecognized requests,
	// or the CLI/subcommand guard) AND the request carries no code intent,
	// running the search would return confident-wrong code — refuse with
	// guidance instead. Routed requests (explore/impact/plan/...) never reach
	// this gate, so "how does dispatch work", "find the NewServer function"
	// and "what breaks if I change X" route exactly as before.
	if tool == "kern_search" || !metaRoutedTools[tool] {
		if !codeIntent(request, strings.ToLower(request)) {
			return "", &NoCodeIntentError{Request: request}
		}
	}
	// Low-confidence fallback (P2): a code-intent request the whole chain
	// fell through to the plain search fallback for — and that is not a
	// plain symbol locator — answers with a ranked-candidates shortlist
	// instead of running a search. The refusal gate above already refused
	// no-code-intent requests; locator requests keep dispatching search
	// (their documented route, pinned by TestHandleRoutingPreserved). If no
	// tool clears the two-token overlap bar, dispatch search as before.
	if v, _ := subArgs[metaroute.ViaFallbackArg].(bool); v {
		delete(subArgs, metaroute.ViaFallbackArg)
		if !metaroute.SearchLocatorIntent(strings.ToLower(request)) {
			if msg, ok := metaroute.CandidateMessage(request, 3); ok {
				return msg, nil
			}
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
		// A missing-required-param error from the routed handler must teach
		// the agent how to deliver that param: through the kern_meta `args`
		// passthrough (F3). "path is required" → args={"path": "..."}.
		// Only the exact `<identifier> is required` shape teaches: a longer
		// prefix ("a license file is required") is prose, not a param name.
		if param, ok := metaroute.RequiredParamName(err.Error()); ok {
			return "", fmt.Errorf("%s — pass %q through kern_meta's args parameter, e.g. args={\"%s\": \"...\"}", err.Error(), param, param)
		}
		return "", err
	}
	elapsedMs := time.Since(start).Milliseconds()
	out := "[kern] classified as: " + tool
	if viaSemantic {
		out += " (semantic fallback)"
	}
	if h.CostHint != nil {
		// Measured wall-clock latency replaces the old static "est Nms" —
		// a fixed constant presented as a latency promise (actual search
		// runs take seconds). The output-token estimate is kept: it sizes
		// the result, it does not promise latency.
		_, estTokens := h.CostHint(tool)
		out += fmt.Sprintf(" · %dms · %d out tokens", elapsedMs, estTokens)
	}
	out += "\n" + result
	if phase != "" {
		out += fmt.Sprintf("\n[phase hint: %s — set KERN_MCP_PHASE=%s to filter the advertised tool list]", phase, phase)
	}
	return out, nil
}

// ExtractSymbol pulls a candidate symbol name from a natural-language request:
// quoted text ("dispatch" or `dispatch`), or a CamelCase / dotted identifier
// token. The implementation lives in metaroute (the router chain uses it);
// this wrapper keeps the meta surface stable for dispatch-side callers.
func ExtractSymbol(request, low string) string {
	return metaroute.ExtractSymbol(request, low)
}

// ExtractAfterColon returns the text after the first colon in the request, or
// the whole request when there is none. Used by log/mask/compress-type tools
// that receive their payload inline after a colon. The implementation lives
// in metaroute; this wrapper keeps the meta surface stable.
func ExtractAfterColon(request, low string) string {
	return metaroute.ExtractAfterColon(request, low)
}
