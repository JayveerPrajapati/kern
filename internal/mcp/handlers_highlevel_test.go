package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
		// F-1: CLI/subcommand questions must not fall into the graph router's
		// "entry points" fallback — they are symbol searches.
		{"cli_dispatch_question", "how does CLI command dispatch work in this repo?", "kern_search", nil},
		{"cli_change_still_impact", "what breaks if I change the CLI dispatch table", "kern_impact", nil},
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

// TestClassifyMetaRequest_Flow routes flow questions to a graph answer
// (report A9): a flow query with no extractable symbol must reach
// kern_entry_points, and one naming a CamelCase symbol must reach kern_walk —
// never the flat kern_search fallback.
func TestClassifyMetaRequest_Flow(t *testing.T) {
	cases := []classifyCase{
		{"flow_no_symbol", "how does the bundle upload flow work end to end?", "kern_entry_points", nil},
		{"flow_sym", "how does the UploadBundle flow work end to end?", "kern_walk", map[string]string{"symbol": "UploadBundle", "depth": "4"}},
		{"workflow_no_symbol", "explain the deployment workflow pipeline", "kern_entry_points", nil},
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

// TestClassifyMetaRequest_ImpactKeepsVerbThreshold pins A8 at the routing
// layer: the impact route still routes on "what breaks" regardless of the
// stoplist, and leaves symbol selection to the downstream resolver.
func TestClassifyMetaRequest_ImpactBreaksVerb(t *testing.T) {
	tool, _ := classifyMetaRequest("what breaks if I remove the translate function from cmaas_controller?")
	if tool != "kern_impact" {
		t.Fatalf("classifyMetaRequest = %q, want kern_impact", tool)
	}
}

// TestHandleRiskRequiresChange pins the D3 kern_risk contract up front: the
// change argument is mandatory and is rejected before any platform/index
// work happens, so a bare server can serve the error.
func TestHandleRiskRequiresChange(t *testing.T) {
	s := newTestServer()
	_, err := s.handleRisk(context.Background(), map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

// TestHandleRiskServesRiskAssessment drives the D3 happy path end to end:
// handleRisk resolves the root through the session index, builds the
// platform, and returns the rendered governance risk assessment prefixed
// with "RISK for: <change>".
func TestHandleRiskServesRiskAssessment(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	// Drain the session's background index save before TempDir cleanup so the
	// .kern persistence goroutine cannot race the RemoveAll (pre-existing
	// flake: "TempDir RemoveAll cleanup: directory not empty").
	defer s.Close()
	out, err := s.handleRisk(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleRisk: %v", err)
	}
	if !strings.HasPrefix(out, "RISK for: Greet\n") {
		t.Errorf("handleRisk output = %q, want prefix %q", out, "RISK for: Greet\n")
	}
	if !strings.Contains(out, "no risks identified") && !strings.Contains(out, "factor:") {
		t.Errorf("handleRisk output = %q, want a rendered risk assessment (factors or explicit no-risk)", out)
	}
}

// TestHandleAnalyzeLens drives the P1-002 lens arg through kern_analyze: a
// valid lens succeeds with the ANALYSIS prefix and task line; an unknown lens
// is rejected with a clear error.
func TestHandleAnalyzeLens(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "security"})
	if err != nil {
		t.Fatalf("handleAnalyze with lens: %v", err)
	}
	if !strings.HasPrefix(out, "ANALYSIS for: Greet\n") {
		t.Errorf("output = %q, want ANALYSIS prefix", out)
	}
	if !strings.Contains(out, "[task: ") {
		t.Errorf("output should carry the task line, got %q", out)
	}

	if _, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Errorf("unknown lens: err = %v, want rejection with 'unknown lens'", err)
	}
}

// TestHandleAnalyzeProfile drives the P1-005 profile arg through kern_analyze:
// machine-json wraps the full analysis output in a JSON envelope; an unknown
// profile is rejected; no profile arg leaves the output byte-identical.
// (The wrapped content is NOT byte-compared against the base run: each
// handleAnalyze call creates a fresh Task, so the [task: <id>] line differs
// between runs — the invariant is the ANALYSIS prefix + task line inside the
// envelope.)
func TestHandleAnalyzeProfile(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	base, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleAnalyze: %v", err)
	}
	if !strings.HasPrefix(base, "ANALYSIS for: Greet\n") {
		t.Fatalf("base output = %q, want ANALYSIS prefix", base)
	}

	js, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "profile": "machine-json"})
	if err != nil {
		t.Fatalf("handleAnalyze machine-json: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("machine-json output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" || m.Format != "json" {
		t.Errorf("decoded profile=%q format=%q; want machine-json/json", m.Profile, m.Format)
	}
	if !strings.HasPrefix(m.Content, "ANALYSIS for: Greet\n") {
		t.Errorf("wrapped content should carry the ANALYSIS prefix, got %q", m.Content)
	}
	if !strings.Contains(m.Content, "[task: ") {
		t.Errorf("wrapped content should carry the task line, got %q", m.Content)
	}

	if _, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "profile": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("unknown profile: err = %v, want rejection with 'unknown profile'", err)
	}
}

// TestHandleAnalyzeProfileAndLensCombined proves the lens and profile args
// compose: with both set, the lensed analysis is wrapped in the machine-json
// envelope (the lens re-ranks facts inside the ANALYSIS content; the profile
// shapes the whole response).
func TestHandleAnalyzeProfileAndLensCombined(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	js, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "security", "profile": "machine-json"})
	if err != nil {
		t.Fatalf("combined lens+profile: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("combined output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" {
		t.Errorf("profile = %q, want machine-json", m.Profile)
	}
	if !strings.Contains(m.Content, "ANALYSIS for: Greet") {
		t.Errorf("content should carry the analysis, got %q", m.Content)
	}
	if !strings.Contains(m.Content, "[task: ") {
		t.Errorf("content should carry the task line, got %q", m.Content)
	}
}

// TestHandleSkillsUserDir asserts that user skills under <root>/.kern/skills
// are listed (marked "(user)") and readable by name, alongside the embedded
// skills. A missing .kern/skills keeps the output identical to today.
func TestHandleSkillsUserDir(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".kern", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: demo\ndescription: Demo user skill\n---\nBody of the demo user skill.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(strings.NewReader(""), io.Discard)

	out, err := s.handleSkills(context.Background(), map[string]any{"root": root})
	if err != nil {
		t.Fatalf("handleSkills list: %v", err)
	}
	if !strings.Contains(out, "- **demo** (user): Demo user skill") {
		t.Errorf("listing missing user skill entry, got:\n%s", out)
	}
	if !strings.Contains(out, "kern-safe-change") {
		t.Errorf("embedded skills missing from listing, got:\n%s", out)
	}

	body, err := s.handleSkills(context.Background(), map[string]any{"root": root, "skill": "demo"})
	if err != nil {
		t.Fatalf("handleSkills read: %v", err)
	}
	if !strings.Contains(body, "Body of the demo user skill.") {
		t.Errorf("reading demo did not return its body, got %q", body)
	}

	// Without .kern/skills the embedded-only listing is unchanged.
	empty, err := s.handleSkills(context.Background(), map[string]any{"root": t.TempDir()})
	if err != nil {
		t.Fatalf("handleSkills list (no user dir): %v", err)
	}
	if strings.Contains(empty, "(user)") {
		t.Errorf("empty user dir must not emit user entries, got:\n%s", empty)
	}
	if !strings.Contains(empty, "# Kern Bundled Agent Skills") {
		t.Errorf("embedded listing header missing, got:\n%s", empty)
	}
}

func TestClassifyMetaRequest_NoteRoutes(t *testing.T) {
	cases := []struct{ in, wantTool, wantAction string }{
		{"validate the notes tree", "kern_note", "validate"},
		{"are the decision notes valid?", "kern_note", "validate"},
		{"list the decision notes", "kern_note", "list"},
		{"show the note inventory", "kern_note", "list"},
	}
	for _, c := range cases {
		tool, args := classifyMetaRequest(c.in)
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
	tool, args := classifyMetaRequest("list the agent skills")
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
		tool, args := classifyMetaRequest(c.in)
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
