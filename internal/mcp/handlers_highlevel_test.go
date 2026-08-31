package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

// classifyCase is one routing expectation for classifyMetaRequest.
type classifyCase struct {
	name     string
	request  string
	wantTool string
	wantArgs map[string]string // subset of args that must be present
}

// TestClassifyMetaRequest_Explore pins the explore-phase routing: a "how
// does X work" request must classify to kern_explore and extract the symbol.
// Note: only quoted/dotted/CamelCase symbols are extracted — a bare lowercase
// symbol like "dispatch" falls back to kern_search by design (extractSymbol's
// pre-existing behavior, not under test here).
func TestClassifyMetaRequest_Explore(t *testing.T) {
	tool, args := classifyMetaRequest("how does NewServer work?")
	if tool != "kern_explore" {
		t.Fatalf("classifyMetaRequest = %q, want kern_explore", tool)
	}
	if got := args["symbol"]; got != "NewServer" {
		t.Errorf("args[symbol] = %q, want %q", got, "NewServer")
	}
}

// TestClassifyMetaRequest_Plan pins the plan-phase routing: "plan X" must
// classify to kern_plan (unless it is an implementation-plan query).
func TestClassifyMetaRequest_Plan(t *testing.T) {
	tool, _ := classifyMetaRequest("plan adding a greet function")
	if tool != "kern_plan" {
		t.Fatalf("classifyMetaRequest = %q, want kern_plan", tool)
	}
}

// TestClassifyMetaRequest_Verify pins the verify-phase routing: "verify X"
// must classify to kern_verify (and kern_verify_output when a claim is named).
// Note: "verify this change" would hit the earlier change→kern_impact branch;
// the classifier's keyword order is intentional and not under test here.
func TestClassifyMetaRequest_Verify(t *testing.T) {
	tool, _ := classifyMetaRequest("verify this")
	if tool != "kern_verify" {
		t.Fatalf("classifyMetaRequest = %q, want kern_verify", tool)
	}
}

// TestClassifyMetaRequest_Search pins the default search fallback for plain
// locate requests. Note: "handler"/"route" intentionally route to
// kern_entry_points, so a symbol-locate query uses the default fallback.
func TestClassifyMetaRequest_Search(t *testing.T) {
	tool, args := classifyMetaRequest("find the login function")
	if tool != "kern_search" {
		t.Fatalf("classifyMetaRequest = %q, want kern_search", tool)
	}
	if got := args["query"]; got != "find the login function" {
		t.Errorf("args[query] = %q, want the full request", got)
	}
}

// TestClassifyMetaRequest_DefaultFallback verifies unrecognized text routes
// to kern_search with the full request as the query.
func TestClassifyMetaRequest_DefaultFallback(t *testing.T) {
	tool, args := classifyMetaRequest("the quick brown fox jumps over the lazy dog")
	if tool != "kern_search" {
		t.Fatalf("classifyMetaRequest = %q, want kern_search", tool)
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
		{"code_graph", "who calls NewServer", "kern_code_graph", map[string]string{"symbol": "NewServer"}},
		{"entry_points", "find the login handler", "kern_entry_points", nil},
		{"verify_output", "verify the claim that x is safe", "kern_verify_output", nil},
		{"safe_delete", "can i delete the Foo function", "kern_safe_delete", map[string]string{"symbol": "Foo"}},
		{"analyze", "analyze adding a new route", "kern_analyze", nil},
		{"dead_code", "is there dead code in this repo", "kern_dead", nil},
		{"project_map", "show me the project map", "kern_project_map", nil},
		{"commitmsg", "generate a commit message", "kern_commitmsg", nil},
		{"explore_qualified_symbol", "how does Server.dispatch work", "kern_explore", map[string]string{"symbol": "Server.dispatch"}},
		{"implementation_plan_falls_back", "show me the implementation plan", "kern_search", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := classifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("classifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got := args[k]; got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestHandleMeta_PhaseArg verifies kern_meta's phase arg: invalid phases are
// rejected before dispatch, valid phases are echoed as a hint in the response.
func TestHandleMeta_PhaseArg(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	// Invalid phase → rejected up front, before any handler runs.
	_, err := s.handleMeta(context.Background(), map[string]any{"request": "show me the savings", "phase": "bogus"})
	if err == nil || !strings.Contains(err.Error(), "phase must be one of") {
		t.Fatalf("invalid phase: got err %v, want rejection with 'phase must be one of'", err)
	}

	// Valid phase → routed normally and the hint is echoed in the response.
	out, err := s.handleMeta(context.Background(), map[string]any{"request": "show me the savings", "phase": "verify"})
	if err != nil {
		t.Fatalf("valid phase handleMeta: %v", err)
	}
	if !strings.Contains(out, "classified as: kern_stats") {
		t.Errorf("expected kern_stats classification, got: %s", out)
	}
	if !strings.Contains(out, "[phase hint: verify — set KERN_MCP_PHASE=verify to filter the advertised tool list]") {
		t.Errorf("expected phase hint in response, got: %s", out)
	}
}
