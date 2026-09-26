package meta_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// classifyCase is one routing expectation for ClassifyMetaRequest.
type classifyCase struct {
	name     string
	request  string
	wantTool string
	wantArgs map[string]string // subset of args that must be present
}

// TestClassifyMetaRequest_Explore pins the explore-phase routing: a "how
// does X work" request must classify to kern_explore and extract the symbol.
// CamelCase symbols come from ExtractSymbol; bare lowercase symbols
// ("dispatch") are picked up by the "how does X work" regex fallback.
func TestClassifyMetaRequest_Explore(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("how does NewServer work?")
	if tool != "kern_explore" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_explore", tool)
	}
	if got := args["symbol"]; got != "NewServer" {
		t.Errorf("args[symbol] = %q, want %q", got, "NewServer")
	}
}

// TestClassifyMetaRequest_ExploreBareLowercase pins the F-fix: "how does
// dispatch work?" names a bare lowercase symbol that ExtractSymbol skips
// (no dot, no CamelCase) — it must still route to kern_explore with symbol
// "dispatch" instead of degrading to kern_search.
func TestClassifyMetaRequest_ExploreBareLowercase(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("how does dispatch work?")
	if tool != "kern_explore" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_explore", tool)
	}
	if got := args["symbol"]; got != "dispatch" {
		t.Errorf("args[symbol] = %q, want %q", got, "dispatch")
	}
}

// TestClassifyMetaRequest_WhyBareLowercase pins the same fallback on the
// "why does X" branch: it routes to kern_why, not kern_search.
func TestClassifyMetaRequest_WhyBareLowercase(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("why does dispatch exist?")
	if tool != "kern_why" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_why", tool)
	}
	if got := args["symbol"]; got != "dispatch" {
		t.Errorf("args[symbol] = %q, want %q", got, "dispatch")
	}
}

// TestClassifyMetaRequest_Plan pins the plan-phase routing: "plan X" must
// classify to kern_plan (unless it is an implementation-plan query).
func TestClassifyMetaRequest_Plan(t *testing.T) {
	tool, _ := meta.ClassifyMetaRequest("plan adding a greet function")
	if tool != "kern_plan" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_plan", tool)
	}
}

// TestClassifyMetaRequest_Verify pins the verify-phase routing: "verify X"
// must classify to kern_verify (and kern_verify_output when a claim is named).
// Note: "verify this change" would hit the earlier change→kern_impact branch;
// the classifier's keyword order is intentional and not under test here.
func TestClassifyMetaRequest_Verify(t *testing.T) {
	tool, _ := meta.ClassifyMetaRequest("verify this")
	if tool != "kern_verify" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_verify", tool)
	}
}

// TestClassifyMetaRequest_Search pins the default search fallback for plain
// locate requests. Note: "handler"/"route" intentionally route to
// kern_entry_points, so a symbol-locate query uses the default fallback.
func TestClassifyMetaRequest_Search(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("find the login function")
	if tool != "kern_search" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_search", tool)
	}
	if got := args["query"]; got != "find the login function" {
		t.Errorf("args[query] = %q, want the full request", got)
	}
}

// TestClassifyMetaRequest_DefaultFallback verifies unrecognized text routes
// to kern_search with the full request as the query.
func TestClassifyMetaRequest_DefaultFallback(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("the quick brown fox jumps over the lazy dog")
	if tool != "kern_search" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_search", tool)
	}
	if got := args["query"]; got != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("args[query] = %q, want the full request", got)
	}
}

// TestClassifyMetaRequest_Branches covers the remaining major classifier
// branches so a routing regression anywhere in the keyword table is caught.
func TestClassifyMetaRequest_Branches(t *testing.T) {
	cases := []classifyCase{
		{"impact", "what breaks if I change dispatch", "kern_impact", nil},
		{"optimize_log", "compress this log: lots of noise here", "kern_optimize_log", map[string]string{"log": "lots of noise here"}},
		{"mask_pii", "mask secrets and pii in: token=abc123", "kern_mask_pii", map[string]string{"text": "token=abc123"}},
		{"arch", "show me the architecture", "kern_arch", nil},
		{"code_graph", "who calls NewServer", "kern_graph", map[string]string{"symbol": "NewServer", "format": "one-line"}},
		{"entry_points", "find the login handler", "kern_entry_points", nil},
		{"verify_output", "verify the claim that x is safe", "kern_verify_output", nil},
		{"safe_delete", "can i delete the Foo function", "kern_safe_delete", map[string]string{"symbol": "Foo"}},
		{"analyze", "analyze adding a new route", "kern_analyze", nil},
		{"dead_code", "is there dead code in this repo", "kern_dead", nil},
		{"project_map", "show me the project map", "kern_project_map", nil},
		{"commitmsg", "generate a commit message", "kern_commitmsg", nil},
		{"explore_qualified_symbol", "how does Server.dispatch work", "kern_explore", map[string]string{"symbol": "Server.dispatch"}},
		{"implementation_plan_routes", "show me the implementation plan", "kern_plan", nil},
		// F-1: CLI/subcommand questions must not fall into the graph router's
		// "entry points" fallback — they are symbol searches.
		{"cli_dispatch_question", "how does CLI command dispatch work in this repo?", "kern_search", nil},
		{"cli_change_still_impact", "what breaks if I change the CLI dispatch table", "kern_impact", nil},
		// F-2: index intents — rebuild/refresh routes to kern_onboard (the
		// tool that builds/refreshes the index), status/freshness to
		// kern_health. "how does the index work" names a bare lowercase
		// symbol now, so it explores the index subsystem instead of
		// degrading to a flat search.
		{"index_rebuild", "refresh and rebuild the index for this repo now so it is fresh at HEAD", "kern_onboard", nil},
		{"index_rebuild_short", "rebuild the index", "kern_onboard", nil},
		{"index_reindex", "reindex this project", "kern_onboard", nil},
		{"index_status", "is the index fresh", "kern_health", nil},
		{"index_health_word", "index health", "kern_health", nil},
		{"index_plain_explores_symbol", "how does the index work", "kern_explore", map[string]string{"symbol": "index"}},
		// LLM provider intents — chain/sampler/status questions route to
		// kern_llm_providers; a "how does the llm provider work" question
		// names the bare lowercase word "llm", which now explores instead of
		// falling back to search.
		{"llm_providers", "list the LLM provider chain and whether a host sampler is connected", "kern_llm_providers", nil},
		{"llm_providers_short", "llm providers", "kern_llm_providers", nil},
		{"host_sampler", "is the host sampler connected", "kern_llm_providers", nil},
		{"which_local_agent", "which local agent should I use", "kern_llm_providers", nil},
		{"llm_question_explores_bare_word", "how does the llm provider work", "kern_explore", map[string]string{"symbol": "llm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := meta.ClassifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("ClassifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got := args[k]; got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestClassifyMetaRequest_Flow(t *testing.T) {
	cases := []classifyCase{
		{"flow_no_symbol", "how does the bundle upload flow work end to end?", "kern_entry_points", nil},
		{"flow_sym", "how does the UploadBundle flow work end to end?", "kern_near", map[string]string{"symbol": "UploadBundle", "depth": "4"}},
		{"workflow_no_symbol", "explain the deployment workflow pipeline", "kern_entry_points", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := meta.ClassifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("ClassifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got := args[k]; got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestClassifyMetaRequest_Retrieval pins the P1/P2/P3 retrieval routing:
// retrieve/resolve/handle requests land on kern_retrieve/kern_resolve, and
// the plan-context phrases land on kern_plan_context. The retrieval router is
// consulted first, so "retrieve context for X" beats the graph router's
// "context for", while "handler" (a substring of "handle") stays on
// kern_entry_points and plain "plan" stays on kern_plan.
func TestClassifyMetaRequest_Retrieval(t *testing.T) {
	cases := []classifyCase{
		{"retrieve_query", "retrieve the context for Greet", "kern_retrieve", map[string]string{"query": "retrieve the context for Greet", "level": "l1"}},
		{"retrieve_plain", "retrieve nearby symbols", "kern_retrieve", map[string]string{"level": "l1"}},
		{"handle_word", "handle the Count symbol", "kern_retrieve", map[string]string{"level": "l1"}},
		{"resolve_handle_id", "resolve handle abc123", "kern_resolve", map[string]string{"handle": "abc123"}},
		{"resolve_id", "resolve a1b2c3d4", "kern_resolve", map[string]string{"handle": "a1b2c3d4"}},
		{"plan_context", "plan context for adding a route", "kern_plan_context", nil},
		{"context_plan", "context plan for the change", "kern_plan_context", nil},
		{"explain_context", "explain context for NewServer", "kern_plan_context", nil},
		{"planner", "use the planner to size the context", "kern_plan_context", nil},
		// Regression guards: existing routes must win where they should.
		{"handler_keeps_entry_points", "find the login handler", "kern_entry_points", nil},
		{"plan_keeps_kern_plan", "plan adding a greet function", "kern_plan", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := meta.ClassifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("ClassifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got := args[k]; got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestClassifyMetaRequest_ImpactKeepsVerbThreshold pins A8 at the routing
// layer: the impact route still routes on "what breaks" regardless of the
// stoplist, and leaves symbol selection to the downstream resolver.
func TestClassifyMetaRequest_ImpactBreaksVerb(t *testing.T) {
	tool, _ := meta.ClassifyMetaRequest("what breaks if I remove the translate function from cmaas_controller?")
	if tool != "kern_impact" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_impact", tool)
	}
}

func TestClassifyMetaRequest_NoteRoutes(t *testing.T) {
	cases := []struct{ in, wantTool, wantAction string }{}
	for _, c := range cases {
		tool, args := meta.ClassifyMetaRequest(c.in)
		if tool != c.wantTool {
			t.Errorf("%q -> tool %q, want %q", c.in, tool, c.wantTool)
			continue
		}
		if got := args["action"]; got != c.wantAction {
			t.Errorf("%q -> action %q, want %q", c.in, got, c.wantAction)
		}
	}
}

func TestClassifyMetaRequest_SkillRoutes(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("list the agent skills")
	if tool != "kern_skill" || args["action"] != "catalog" {
		t.Errorf("skill list -> %q %v, want kern_skill catalog", tool, args)
	}
}

func TestClassifyMetaRequest_SkillLoadRoutes(t *testing.T) {
	cases := []struct{ in, wantTool, wantSkill string }{
		// Bare "safe change" without skill language deliberately stays
		// kern_impact (impact analysis is the better answer than loading the
		// runbook); explicit skill language routes to kern_skill load.
		{"how do i make a safe change here", "kern_impact", ""},
		{"use the incident triage skill", "kern_skill", "kern-incident-triage"},
		{"show me the kern-safe-change runbook", "kern_skill", "kern-safe-change"},
		{"what skills exist", "kern_skill", ""}, // generic -> catalog
	}
	for _, c := range cases {
		tool, args := meta.ClassifyMetaRequest(c.in)
		if tool != c.wantTool {
			t.Errorf("%q -> tool %q, want %q", c.in, tool, c.wantTool)
			continue
		}
		if c.wantTool != "kern_skill" {
			continue
		}
		if c.wantSkill == "" {
			if args["action"] != "catalog" {
				t.Errorf("%q -> action %v, want catalog", c.in, args["action"])
			}
			continue
		}
		if args["action"] != "load" || args["skill"] != c.wantSkill {
			t.Errorf("%q -> %v, want load %s", c.in, args, c.wantSkill)
		}
	}
}

// TestClassifyProjectTools_FitContext pins the project-router fit-context
// branch (moved from the root fixture test TestFitContextViaMCP step 3).
// Note: kern_fit_context has no handleMeta dispatch arm, so Handle rewrites
// such routes to kern_search — the classifier itself still names the tool.
func TestClassifyProjectTools_FitContext(t *testing.T) {
	tool, args, ok := meta.ClassifyProjectTools("fit context for web server", "fit context for web server")
	if !ok || tool != "kern_fit_context" {
		t.Errorf("expected ClassifyProjectTools to route to kern_fit_context, got tool=%s, ok=%v, args=%v", tool, ok, args)
	}
	if args["query"] != "fit context for web server" {
		t.Errorf("args[query] = %v, want the raw request", args["query"])
	}
}

// TestKnownVerifyType pins the verify-type vocabulary: every token the engine
// can run (including the aliases its substring dispatch accepts) is known,
// and garbage tokens (e.g. types=123, a number coerced to "123") are not.
func TestKnownVerifyType(t *testing.T) {
	valid := []string{
		"build", "test", "security", "architecture", "dependency",
		"e2e", "static-analysis", "performance", "ci",
		// Aliases the engine's substring dispatch accepts.
		"unit", "integration", "sec", "archi", "dep", "vet", "lint", "bench", "end-to-end",
	}
	for _, v := range valid {
		if !meta.KnownVerifyType(v) {
			t.Errorf("KnownVerifyType(%q) = false, want true", v)
		}
	}
	invalid := []string{"123", "garbage", "zzz", "foo bar"}
	for _, v := range invalid {
		if meta.KnownVerifyType(v) {
			t.Errorf("KnownVerifyType(%q) = true, want false", v)
		}
	}
}

// TestClassifyMetaRequest_AuditLog pins N1a: audit-intent requests route to
// kern_audit instead of falling through the semantic fallback to a
// symbol-search dead end.
func TestClassifyMetaRequest_AuditLog(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("audit log")
	if tool != "kern_audit" {
		t.Fatalf("ClassifyMetaRequest(%q) = %q, want kern_audit", "audit log", tool)
	}
	if len(args) != 0 {
		t.Errorf("kern_audit args = %v, want empty", args)
	}
}

// TestClassifyMetaRequest_AuditTrail pins the adjacent-word routing for the
// other audit-intent phrases: "trail" (and entries/history) work exactly like
// "log".
func TestClassifyMetaRequest_AuditTrail(t *testing.T) {
	for _, req := range []string{
		"show me the audit trail",
		"show the audit",
		"audit what happened",
		"list the audit entries",
		"audit history for the last week",
	} {
		tool, _ := meta.ClassifyMetaRequest(req)
		if tool != "kern_audit" {
			t.Errorf("ClassifyMetaRequest(%q) = %q, want kern_audit", req, tool)
		}
	}
}

// TestClassifyMetaRequest_AuditSymbolNotHijacked pins the N1a word-boundary
// guard: "AuditLog" is ONE word (a symbol), so a symbol question about it
// must keep routing to kern_explore — never kern_audit. The same holds for
// the all-lowercase single-token form.
func TestClassifyMetaRequest_AuditSymbolNotHijacked(t *testing.T) {
	for _, req := range []string{
		"how does AuditLog work",
		"how does auditlog work",
	} {
		tool, _ := meta.ClassifyMetaRequest(req)
		if tool == "kern_audit" {
			t.Errorf("ClassifyMetaRequest(%q) = kern_audit, must stay a symbol question", req)
		}
		if tool != "kern_explore" {
			t.Errorf("ClassifyMetaRequest(%q) = %q, want kern_explore (unchanged routing)", req, tool)
		}
	}
}

// TestHandleToolCatalogHook pins N1b: with a ToolCatalog hook wired, a
// tool-catalog request renders the catalog table directly (no classification
// fall-through).
func TestHandleToolCatalogHook(t *testing.T) {
	h := meta.Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			return "routed:" + name, nil
		},
		ToolCatalog: func() (string, error) {
			return "kern_meta  cross  low  Natural-language router\nkern_search explore low  Ranked symbol search", nil
		},
	}
	out, err := meta.Handle(context.Background(), h, map[string]any{"request": "give me the full tool catalog"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(out, "[kern] tool catalog (2 tools):") {
		t.Errorf("expected catalog prefix with tool count, got: %q", out)
	}
	if !strings.Contains(out, "kern_search explore low") {
		t.Errorf("expected the stub catalog rendering, got: %q", out)
	}
}

// TestHandleToolCatalogNilHookUnchanged pins N1b fallback: with a nil hook
// the same request classifies exactly as before (the hook must never change
// behavior when the server did not wire it).
func TestHandleToolCatalogNilHookUnchanged(t *testing.T) {
	h := meta.Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			return "routed:" + name, nil
		},
	}
	out, err := meta.Handle(context.Background(), h, map[string]any{"request": "give me the full tool catalog"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(out, "[kern] tool catalog") {
		t.Errorf("nil hook must not render the catalog: %q", out)
	}
	if !strings.Contains(out, "[kern] classified as:") {
		t.Errorf("nil hook must fall through to classification, got: %q", out)
	}
}

// TestClassifyMetaRequest_StatusOfSymbol pins N8: "status of X" where X is a
// symbol (dot-qualified or CamelCase in the original request) routes to
// kern_explore with the extracted symbol — a symbol's status is its
// definition + callers + blast radius, not project health. Non-symbol status
// phrasings ("index status", "status of the project", "build status",
// "health" alone) keep the pinned kern_health routing.
func TestClassifyMetaRequest_StatusOfSymbol(t *testing.T) {
	cases := []classifyCase{
		{"status_of_qualified", "status of Server.dispatch", "kern_explore", map[string]string{"symbol": "Server.dispatch"}},
		{"status_of_camel", "what's the status of TaskService", "kern_explore", map[string]string{"symbol": "TaskService"}},
		{"status_of_long_form", "what is the status of TaskService", "kern_explore", map[string]string{"symbol": "TaskService"}},
		{"status_of_article_camel", "status of the TaskService", "kern_explore", map[string]string{"symbol": "TaskService"}},
		{"index_status_stays_health", "index status", "kern_health", nil},
		{"status_of_project_stays_health", "status of the project", "kern_health", nil},
		{"status_of_index_stays_health", "status of the index", "kern_health", nil},
		{"build_status_stays_health", "build status", "kern_health", nil},
		{"health_alone_stays_health", "health", "kern_health", nil},
		{"status_of_lowercase_not_symbol", "status of dispatch", "kern_health", nil},
		{"status_of_single_letter", "status of B", "kern_explore", map[string]string{"symbol": "B"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := meta.ClassifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("ClassifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got := args[k]; got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestHandleToolCatalogLongTail pins the N-followup catalog-vocabulary
// long-tail: the natural phrasings ("list the mcp tools", "show me the
// tools please", "list tools", "tool inventory") render the catalog instead
// of dead-ending into the symbol-search fallback. A symbol question about
// ONE tool's behavior ("how does kern_search clip results") must NOT be
// hijacked by the catalog hook.
func TestHandleToolCatalogLongTail(t *testing.T) {
	h := meta.Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			return "routed:" + name, nil
		},
		ToolCatalog: func() (string, error) {
			return "kern_search explore low  Ranked symbol search\nkern_arch explore low  Architecture overview", nil
		},
	}
	for _, req := range []string{
		"list the mcp tools",
		"show me the tools please",
		"list tools",
		"what is the tool inventory",
	} {
		out, err := meta.Handle(context.Background(), h, map[string]any{"request": req})
		if err != nil {
			t.Fatalf("Handle(%q): %v", req, err)
		}
		if !strings.Contains(out, "[kern] tool catalog") {
			t.Errorf("%q must render the catalog, got: %q", req, out)
		}
	}
	// A request about a specific tool's behavior keeps its symbol routing.
	out, err := meta.Handle(context.Background(), h, map[string]any{"request": "how does kern_search clip results"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(out, "[kern] tool catalog") {
		t.Errorf("symbol question must not render the catalog, got: %q", out)
	}
	if !strings.Contains(out, "[kern] classified as:") {
		t.Errorf("symbol question must classify, got: %q", out)
	}
}
