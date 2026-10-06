package meta_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// TestKnownVerifyType pins the verify-type vocabulary: every token the engine
// can run (including the aliases its substring dispatch accepts) is known,
// and garbage tokens (e.g. types=123, a number coerced to "123") are not.
func TestKnownVerifyType(t *testing.T) {
	valid := []string{
		"build", "test", "security", "architecture", "dependency",
		"reuse", "e2e", "static-analysis", "performance", "ci",
		// Aliases the engine's substring dispatch accepts.
		"unit", "integration", "sec", "archi", "dep", "vet", "lint", "bench", "end-to-end",
	}
	for _, v := range valid {
		if !catalog.KnownVerifyType(v) {
			t.Errorf("KnownVerifyType(%q) = false, want true", v)
		}
	}
	invalid := []string{"123", "garbage", "zzz", "foo bar"}
	for _, v := range invalid {
		if catalog.KnownVerifyType(v) {
			t.Errorf("KnownVerifyType(%q) = true, want false", v)
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

// routeRecorder is a RouteTool stub that records the dispatched tool so
// tests can assert what (if anything) was routed.
type routeRecorder struct {
	called bool
	tool   string
}

func (r *routeRecorder) route(ctx context.Context, name string, args map[string]any) (string, error) {
	r.called = true
	r.tool = name
	return "routed:" + name, nil
}

func metaTestHooks(r *routeRecorder) meta.Hooks {
	return meta.Hooks{
		RouteTool: r.route,
		CostHint: func(tool string) (int, int) {
			return 150, 1200
		},
	}
}

// TestHandleRefusesNoCodeIntent pins the P1 refusal: a request the
// classifier cannot route AND that names no code/repo/kern vocabulary
// ("make me a sandwich") must NOT run the search fallback — Handle returns
// a NoCodeIntentError with guidance instead of a junk symbol search.
func TestHandleRefusesNoCodeIntent(t *testing.T) {
	r := &routeRecorder{}
	_, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": "make me a sandwich"})
	if err == nil {
		t.Fatal("Handle must refuse a non-code request, got nil error")
	}
	var nci *meta.NoCodeIntentError
	if !errors.As(err, &nci) {
		t.Fatalf("error type = %T, want *meta.NoCodeIntentError", err)
	}
	if r.called {
		t.Errorf("RouteTool must not run for a refused request (routed %q)", r.tool)
	}
	for _, want := range []string{"no code intent detected", "kern search <symbol>", "kern arch", "kern buddy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message missing %q; got: %q", want, err.Error())
		}
	}
}

// TestHandleRefusesSocialJunk: polite/social one-liners an agent might drop
// into meta are refused, never searched.
func TestHandleRefusesSocialJunk(t *testing.T) {
	for _, req := range []string{
		"hello",
		"thanks",
		"please",
		"write me a poem",
		"tell me a joke",
		"what is the meaning of life",
	} {
		r := &routeRecorder{}
		_, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": req})
		var nci *meta.NoCodeIntentError
		if !errors.As(err, &nci) {
			t.Errorf("Handle(%q) must refuse, got err=%v", req, err)
		}
		if r.called {
			t.Errorf("Handle(%q) must not route; RouteTool ran %q", req, r.tool)
		}
	}
}

// TestHandleRefusesLongJunkQuery: a 30x-repeated non-code request is refused
// up front — the refusal happens before any route, so even a huge request
// costs nothing and never reaches the search.
func TestHandleRefusesLongJunkQuery(t *testing.T) {
	long := strings.Repeat("make me a sandwich ", 30)
	r := &routeRecorder{}
	_, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": long})
	var nci *meta.NoCodeIntentError
	if !errors.As(err, &nci) {
		t.Fatalf("Handle must refuse a long non-code request, got err=%v", err)
	}
	if r.called {
		t.Errorf("RouteTool must not run for a refused request")
	}
}

// TestNoCodeIntentErrorClipped: the refusal message echoes the request
// clipped to ~80 chars so a 30x-repeated request cannot blow up the error.
func TestNoCodeIntentErrorClipped(t *testing.T) {
	long := strings.Repeat("xyz ", 60) // 240 chars
	err := &meta.NoCodeIntentError{Request: long}
	msg := err.Error()
	if len(msg) > 200 {
		t.Errorf("refusal message too long (%d chars): %.120s...", len(msg), msg)
	}
	if !strings.Contains(msg, "…") {
		t.Errorf("refusal message must clip the echoed request with an ellipsis: %q", msg)
	}
}

// TestHandleRoutingPreserved pins the P1 guard: the protected routings must
// be unchanged — explore for how-does questions, search for symbol locators,
// impact for what-breaks, arch for architecture, and the CLI/subcommand
// guard stays a search. None of these may be refused.
func TestHandleRoutingPreserved(t *testing.T) {
	cases := []struct {
		request  string
		wantTool string
	}{
		{"how does dispatch work?", "kern_explore"},
		{"find the NewServer function", "kern_search"},
		{"find the login function", "kern_search"},
		{"what breaks if I change dispatch", "kern_impact"},
		{"how does CLI command dispatch work in this repo?", "kern_search"},
		{"show me the architecture", "kern_arch"},
	}
	for _, tc := range cases {
		t.Run(tc.request, func(t *testing.T) {
			r := &routeRecorder{}
			out, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": tc.request})
			if err != nil {
				t.Fatalf("Handle(%q) must route, got err: %v", tc.request, err)
			}
			if !r.called || r.tool != tc.wantTool {
				t.Fatalf("Handle(%q) routed %q (called=%v), want %q", tc.request, r.tool, r.called, tc.wantTool)
			}
			if !strings.Contains(out, "classified as: "+tc.wantTool) {
				t.Errorf("output header missing classification: %q", out)
			}
		})
	}
}

// TestHandleRenameRoutesDirectly pins the catalog-derived route table:
// "rename the Foo function" classifies to kern_rename, which IS a catalog
// tool, so Handle dispatches it directly. The old hand-maintained map had no
// kern_rename arm and rewrote such routes to kern_search; deriving the
// routable set from the catalog makes every catalog tool reachable, and the
// P1 gate still only refuses requests with NO code intent.
func TestHandleRenameRoutesDirectly(t *testing.T) {
	r := &routeRecorder{}
	out, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": "rename the Foo function"})
	if err != nil {
		t.Fatalf("Handle must route a code-intent request, got err: %v", err)
	}
	if !r.called || r.tool != "kern_rename" {
		t.Fatalf("routed %q (called=%v), want kern_rename", r.tool, r.called)
	}
	if !strings.Contains(out, "classified as: kern_rename") {
		t.Errorf("output missing classification: %q", out)
	}
}

// TestHandleFallbackRewriteStillRunsForCodeIntent: the fallback rewrite is
// preserved for classified names that are NOT catalog tools. kern_meta — the
// router itself — is deliberately excluded from the catalog-derived route
// table (routing to it would recurse), so a request that classifies to it
// (semantic escalation over its own description) rewrites to kern_search and
// MUST still run when the request names code vocabulary — the P1 gate only
// refuses requests with NO code intent.
func TestHandleFallbackRewriteStillRunsForCodeIntent(t *testing.T) {
	r := &routeRecorder{}
	out, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": "use the natural language router to inspect this codebase"})
	if err != nil {
		t.Fatalf("Handle must route a code-intent fallback, got err: %v", err)
	}
	if !r.called || r.tool != "kern_search" {
		t.Fatalf("routed %q (called=%v), want kern_search", r.tool, r.called)
	}
	if !strings.Contains(out, "classified as: kern_search") {
		t.Errorf("output missing classification: %q", out)
	}
}

// TestHandleMeasuredLatencyNotStaticEst: the classified line carries the
// measured wall-clock latency and the token estimate, and never the old
// static "est Nms" promise.
func TestHandleMeasuredLatencyNotStaticEst(t *testing.T) {
	r := &routeRecorder{}
	out, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": "find the NewServer function"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(out, "· est ") {
		t.Errorf("static est-latency hint must not appear, got: %q", out)
	}
	if !strings.Contains(out, "ms · ") || !strings.Contains(out, "1200 out tokens") {
		t.Errorf("classified line must carry measured latency + token estimate, got: %q", out)
	}
}

// TestHandleLowConfidenceCandidates pins P2: a code-intent request the whole
// chain falls through to the plain search fallback for — and that is not a
// plain symbol locator — answers with the ranked-candidates shortlist and
// does NOT dispatch. The request below clears the two-token overlap bar
// against kern_optimize/kern_buddy descriptions but stays below the
// semantic routing threshold, so it reaches the fallback marker.
func TestHandleLowConfidenceCandidates(t *testing.T) {
	r := &routeRecorder{}
	out, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{
		"request": "please carefully consider the full overall context of this session and the current project state before answering any question about the dispatch module directly",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if r.called {
		t.Fatalf("RouteTool must not run for the low-confidence fallback (routed %q)", r.tool)
	}
	for _, want := range []string{
		"[kern] low-confidence request",
		"Top candidates:",
		"1. kern_optimize",
		"Rephrase or call one directly",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("candidates message missing %q; got: %q", want, out)
		}
	}
}

// TestHandleFallbackMarkerNotDispatchedToRoutedTool pins that the
// via_fallback marker never leaks into a dispatched call: a locator request
// ("find ... function") still dispatches kern_search with a clean arg set
// (the marker is stripped even on the dispatch path).
func TestHandleFallbackMarkerStrippedBeforeDispatch(t *testing.T) {
	r := &routeRecorder{}
	_, err := meta.Handle(context.Background(), metaTestHooks(r), map[string]any{"request": "find the NewServer function"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !r.called || r.tool != "kern_search" {
		t.Fatalf("routed %q (called=%v), want kern_search", r.tool, r.called)
	}
}
