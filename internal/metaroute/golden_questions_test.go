package metaroute_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/metaroute"
)

// TestClassifyMetaRequest_GoldenQuestions pins the Phase-1 routing-quality
// campaign: the live-probed misroutes (flow questions landing on
// kern_entry_points, "why is the code slow" falling to kern_search), the
// eval's phrasings, the perf-keyword and layering additions, and negative
// hijack guards (workflow/pprof-trace/locator/layering-violations must keep
// their existing routes). classifyCase is the shared fixture type from
// router_test.go (same package).
func TestClassifyMetaRequest_GoldenQuestions(t *testing.T) {
	cases := []classifyCase{
		// Live-probed misroutes, now correct.
		{"flow_trace_through_system", "trace the flow of dispatch through the system", "kern_explore", map[string]string{"symbol": "dispatch"}},
		{"why_is_code_slow", "why is the code slow", "kern_fragility_hotspots", nil},
		{"connect_end_to_end", "how do authentication and session handling connect end to end", "kern_path", map[string]string{"from": "authentication", "to": "session"}},
		{"dependency_cycles", "are there dependency cycles", "kern_cycles", nil},
		{"performance_hotspots", "where are the performance hotspots", "kern_fragility_hotspots", nil},
		{"fragile_components", "find fragile components", "kern_fragility_hotspots", nil},
		{"perf_keywords_hotspot", "find performance hotspots and profile slow paths", "kern_fragility_hotspots", nil},

		// Eval phrasings.
		{"eval_trace_flow_of", "trace the flow of dispatch", "kern_explore", map[string]string{"symbol": "dispatch"}},
		{"eval_dependency_cycles", "dependency cycles", "kern_cycles", nil},
		{"eval_performance_hotspots", "performance hotspots", "kern_fragility_hotspots", nil},
		{"eval_layering", "layering", "kern_cycles", nil},
		{"layering_question", "check the layering of this project", "kern_cycles", nil},
		{"layering_what_is", "what is the layering like", "kern_cycles", nil},

		// Flow routing: two-symbol → kern_path, one-symbol → kern_explore,
		// zero-symbol → kern_explain.
		{"flow_between", "trace the flow between dispatch and storage", "kern_path", map[string]string{"from": "dispatch", "to": "storage"}},
		{"flow_from_to", "follow the flow from login to checkout", "kern_path", map[string]string{"from": "login", "to": "checkout"}},
		{"flow_from_transitive", "how does the request flow from login to checkout", "kern_path", map[string]string{"from": "login", "to": "checkout"}},
		{"flow_connect_articles", "how do the services and the workers connect end to end", "kern_path", map[string]string{"from": "services", "to": "workers"}},
		// Retrieval-arm hijack guard (remediation): the retrieval router
		// substring-matches "handle" for "resolve handle <id>" requests;
		// the flow arm runs BEFORE retrieval so handle* symbols in a flow
		// question keep their kern_path route, and the captured from/to keep
		// their ORIGINAL casing (intel.Resolve is case-sensitive).
		{"flow_with_handle_symbols", "how do handleCycles and handlePath connect end to end", "kern_path", map[string]string{"from": "handleCycles", "to": "handlePath"}},
		{"flow_one_symbol_camel", "trace the flow of NewServer", "kern_explore", map[string]string{"symbol": "NewServer"}},
		{"flow_zero_symbol", "follow the flow", "kern_explain", nil},
		{"flow_through_one_symbol", "trace how data flows through dispatch", "kern_explore", map[string]string{"symbol": "dispatch"}},

		// Negative / hijack guards — existing routes must win.
		{"workflow_not_hijacked", "run the workflow for auth", "kern_entry_points", nil},
		{"run_this_workflow_not_hijacked", "run this workflow", "kern_run", nil},
		{"pprof_trace_not_hijacked", "trace this pprof stack", "kern_trace", nil},
		{"login_handler_not_hijacked", "find the login handler", "kern_entry_points", nil},
		{"impact_not_hijacked", "what breaks if I change dispatch", "kern_impact", nil},
		{"flow_end_to_end_no_connect_keeps_entry_points", "how does the bundle upload flow work end to end?", "kern_entry_points", nil},
		{"flow_end_to_end_symbol_keeps_near", "how does the UploadBundle flow work end to end?", "kern_near", map[string]string{"symbol": "UploadBundle", "depth": "4"}},
		// Route-path flow questions belong to the structure arm (route
		// search), never the flow arm — the flow arm defers to the structure
		// arm whenever the request carries an HTTP route.
		{"route_path_flow_not_hijacked", "trace the flow of requests to /v1/loop", "kern_search", map[string]string{"query": "/v1/loop"}},
		{"route_path_follow_flow_not_hijacked", "follow the flow for /api/users/:id", "kern_search", map[string]string{"query": "/api/users/:id"}},
		{"layering_violations_keep_verify", "check for layering violations", "kern_verify", map[string]string{"types": "architecture"}},

		// Spread across the already-good arms (phrasings from router_test.go).
		{"explore_symbol", "how does NewServer work", "kern_explore", map[string]string{"symbol": "NewServer"}},
		{"plan_arm", "plan adding a greet function", "kern_plan", nil},
		{"verify_arm", "verify this", "kern_verify", nil},
		{"search_locator", "find the login function", "kern_search", nil},
		{"graph_callers", "who calls NewServer", "kern_graph", map[string]string{"symbol": "NewServer", "format": "one-line"}},
		{"cli_guard_search", "how does CLI command dispatch work in this repo?", "kern_search", nil},
		{"arch_overview", "show me the architecture", "kern_arch", nil},
		{"dead_code_arm", "is there dead code in this repo", "kern_dead", nil},
		{"implementation_plan_arm", "show me the implementation plan", "kern_plan", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := metaroute.ClassifyMetaRequest(tc.request)
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
