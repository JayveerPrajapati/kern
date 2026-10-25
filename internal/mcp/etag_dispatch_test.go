package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcpserve"
	"github.com/JayveerPrajapati/kern/internal/pii"
)

// resultField reads a top-level field off a tools/call result object.
func resultField(t *testing.T, resp map[string]any, key string) any {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	return res[key]
}

// TestConditionalFetchEtagOnEligibleResponse (a): an eligible tool response
// carries its etag even when the caller passed no etag — the value is the
// sha256 of the raw response text combined with the tool's schema version
// and the serve-time view (max_output budget, F-2).
func TestConditionalFetchEtagOnEligibleResponse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"root": root, "path": f})
	resp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	e, ok := resultField(t, resp, "etag").(string)
	if !ok || e == "" {
		t.Fatalf("eligible response must carry an etag, got %+v", resp)
	}
	// The served etag folds in the same effective budget the server used
	// (per-tool default for kern_compact_file, R7).
	budget, err := mcpserve.CallOutputBudget("kern_compact_file", map[string]any{"root": root, "path": f})
	if err != nil {
		t.Fatal(err)
	}
	if got := etag.HashView(out, SchemaVersionV1, strconv.Itoa(budget)); got != e {
		t.Fatalf("etag mismatch: response %q but recompute %q", e, got)
	}
	if resultField(t, resp, "unchanged") != nil {
		t.Fatalf("fresh response must not be marked unchanged: %+v", resp)
	}
}

// TestConditionalFetchShortCircuit (b): a second call passing etag=<previous>
// is answered with the tiny unchanged response (unchanged=true), not the full
// text. Serving from the D1 cache then short-circuiting is expected.
func TestConditionalFetchShortCircuit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"root": root, "path": f}
	args1, _ := json.Marshal(base)
	resp1 := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args1)+`}`))
	text1, isErr := toolResultText(t, resp1)
	if isErr {
		t.Fatalf("unexpected error: %s", text1)
	}
	e := resultField(t, resp1, "etag").(string)
	if e == "" {
		t.Fatal("first response must carry an etag")
	}

	args2, _ := json.Marshal(map[string]any{"root": root, "path": f, "etag": e})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args2)+`}`))
	text2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error on unchanged call: %s", text2)
	}
	if !strings.HasPrefix(text2, "unchanged (etag ") {
		t.Fatalf("expected the unchanged short-circuit text, got %q", text2)
	}
	if got := resultField(t, resp2, "unchanged"); got != true {
		t.Fatalf("unchanged response must set unchanged=true, got %v", got)
	}
	if got := resultField(t, resp2, "etag"); got != e {
		t.Fatalf("unchanged response must echo the matched etag, got %v", got)
	}
}

// TestConditionalFetchEtagRotatesOnContentChange (c): editing the fixture
// file mints a new etag, and a call with the stale etag is re-served in full.
func TestConditionalFetchEtagRotatesOnContentChange(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"root": root, "path": f}
	args1, _ := json.Marshal(base)
	resp1 := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args1)+`}`))
	e1 := resultField(t, resp1, "etag").(string)

	// Modify the file → the summary changes → a fresh etag.
	if err := os.WriteFile(f, []byte("package main\n\nfunc bar() string { return \"bye\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args2, _ := json.Marshal(map[string]any{"root": root, "path": f, "etag": e1, "no_cache": "1"})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args2)+`}`))
	text2, isErr := toolResultText(t, resp2)
	if isErr {
		t.Fatalf("unexpected error: %s", text2)
	}
	if strings.HasPrefix(text2, "unchanged (etag ") {
		t.Fatalf("stale etag must NOT short-circuit after a content change, got %q", text2)
	}
	if !strings.Contains(text2, "bar") {
		t.Fatalf("expected the re-served full summary with the new symbol, got %q", text2)
	}
	e2 := resultField(t, resp2, "etag").(string)
	if e2 == e1 {
		t.Fatal("content change must mint a different etag")
	}
}

// TestConditionalFetchEtagRotatesOnFileEditViaCacheHit (F-1): kern_compact_file
// is a file-backed Cacheable tool with no index identity to rotate the D1
// cache key, so the key must carry a fingerprint of the target file. Editing
// the file between two calls — the second WITHOUT no_cache (the D1-hit path)
// — must rotate the key, re-serve the fresh summary and mint a new etag,
// never answer "unchanged" from the stale cached entry.
func TestConditionalFetchEtagRotatesOnFileEditViaCacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"root": root, "path": f}
	args1, _ := json.Marshal(base)
	resp1 := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args1)+`}`))
	text1, isErr := toolResultText(t, resp1)
	if isErr {
		t.Fatalf("unexpected error: %s", text1)
	}
	e1 := resultField(t, resp1, "etag").(string)
	if e1 == "" {
		t.Fatal("first response must carry an etag")
	}
	if !strings.Contains(text1, "foo") {
		t.Fatalf("expected the initial summary to mention foo, got %q", text1)
	}

	// Edit the file → the summary changes (different size so the fingerprint
	// rotates even on coarse-mtime filesystems).
	if err := os.WriteFile(f, []byte("package main\n\nfunc bar() string { return \"bye\" }\nvar extra = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// NO no_cache here: the D1-hit path itself must notice the edit via the
	// file-fingerprinted key and re-serve the fresh full summary.
	args2, _ := json.Marshal(map[string]any{"root": root, "path": f, "etag": e1})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args2)+`}`))
	text2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error: %s", text2)
	}
	if strings.HasPrefix(text2, "unchanged (etag ") {
		t.Fatalf("an edited file must NOT be answered unchanged from the D1 cache, got %q", text2)
	}
	if !strings.Contains(text2, "bar") {
		t.Fatalf("expected the fresh summary with the new symbol, got %q", text2)
	}
	e2 := resultField(t, resp2, "etag").(string)
	if e2 == e1 {
		t.Fatal("content change must mint a different etag even on the cache path")
	}
}

// TestConditionalFetchCacheKeyIgnoresEtag (d): the etag argument (and the
// identity-only args) are conditional-fetch/identity concerns (F8 pattern) —
// they must not change the D1 cache key, so identical asks share one entry
// across etag variance. max_output is the exception (F-2): the etag is bound
// to the serve-time view, so different serve views are different content and
// MUST key separate entries.
func TestConditionalFetchCacheKeyIgnoresEtag(t *testing.T) {
	t.Parallel()
	base := map[string]any{"root": "/repo", "path": "/repo/main.go"}
	withEtag := map[string]any{"root": "/repo", "path": "/repo/main.go", "etag": "deadbeef", "agent_id": "alice"}
	if cacheKeyFor(context.Background(), "kern_compact_file", base, "/repo", "noindex") != cacheKeyFor(context.Background(), "kern_compact_file", withEtag, "/repo", "noindex") {
		t.Fatal("etag/identity args must not change the D1 cache key")
	}
	withMaxOutput := map[string]any{"root": "/repo", "path": "/repo/main.go", "max_output": "100"}
	if cacheKeyFor(context.Background(), "kern_compact_file", base, "/repo", "noindex") == cacheKeyFor(context.Background(), "kern_compact_file", withMaxOutput, "/repo", "noindex") {
		t.Fatal("max_output must change the D1 cache key (the etag is view-bound, F-2)")
	}
	if cacheKeyFor(context.Background(), "kern_compact_file", base, "/repo", "noindex") == cacheKeyFor(context.Background(), "kern_compact_file", map[string]any{"root": "/repo", "path": "/repo/other.go"}, "/repo", "noindex") {
		t.Fatal("a real argument difference must change the cache key")
	}
}

// TestConditionalFetchShortCircuitNotStored (e): a short-circuit response is
// never persisted to the D1 cache — the stored entry stays the full text
// (the design's "skip cacheStore for short-circuit responses").
func TestConditionalFetchShortCircuitNotStored(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"root": root, "path": f}
	args1, _ := json.Marshal(base)
	resp1 := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args1)+`}`))
	text1, isErr := toolResultText(t, resp1)
	if isErr {
		t.Fatalf("unexpected error: %s", text1)
	}
	e := resultField(t, resp1, "etag").(string)

	args2, _ := json.Marshal(map[string]any{"root": root, "path": f, "etag": e})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args2)+`}`))
	out2, isErr2 := toolResultText(t, resp2)
	if isErr2 || !strings.HasPrefix(out2, "unchanged (etag ") {
		t.Fatalf("expected the unchanged short-circuit, got %q (err %v)", out2, isErr2)
	}

	// The D1 entry (kern_compact_file builds no index → identity "noindex")
	// must still hold the FULL text, never the short response.
	key := toolCacheKeyPrefix + cacheKeyFor(context.Background(), "kern_compact_file", base, resolveRoot(root), "noindex")
	var entry toolCacheEntry
	if err := cache.Load(key, &entry); err != nil {
		t.Fatalf("expected the full response stored in D1, got %v", err)
	}
	if entry.Text != pii.Mask(text1).Text {
		t.Fatalf("stored entry must equal the masked original full text, got %q", entry.Text)
	}
	if strings.Contains(entry.Text, "unchanged (etag ") {
		t.Fatalf("short-circuit response must not be stored into D1, got %q", entry.Text)
	}
}

// TestConditionalFetchWorkingSetAndMetaRoute (f): eligible responses record
// the caller's working set, and the kern_meta "working set" route lists it.
// The route is non-cacheable and the classifier routes to the marker.
func TestConditionalFetchWorkingSetAndMetaRoute(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Registry records under the caller's agent_id bucket. The agent must be
	// registered: kern_compact_file now consults the per-call governor (C6),
	// and an unregistered agent_id fails closed.
	agent := registerP12Agent(t)
	args, _ := json.Marshal(map[string]any{"root": root, "path": f, "agent_id": agent})
	resp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args)+`}`))
	text1, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", text1)
	}
	e := resultField(t, resp, "etag").(string)

	entries := etag.Default.Entries(agent)
	found := false
	for _, en := range entries {
		if en.Tool == "kern_compact_file" && en.ETag == e {
			found = true
		}
	}
	if !found {
		t.Fatalf("working set must record (kern_compact_file, %s), got %+v", e, entries)
	}

	// The classifier routes workingset requests to the marker.
	routed, rargs := classifyMetaRequest("my working set")
	if routed != "kern_context" || !argBool(rargs, "workingset") {
		t.Fatalf("classifyMetaRequest('my working set') = %s %v, want the workingset marker", routed, rargs)
	}
	if !func() bool { r, _ := classifyMetaRequest("workingset"); return r == "kern_context" }() {
		t.Fatalf("bare 'workingset' request must route to the workingset marker")
	}
	if cacheableForCall(context.Background(), "kern_meta", map[string]any{"request": "my working set"}) {
		t.Fatal("the workingset route must never be D1-cacheable")
	}

	// Full dispatch: kern_meta "my working set" renders the registry. The
	// call carries the same agent_id as the recording call above — the
	// workingset route renders the caller's OWN bucket (meta.Handle derives
	// it from the meta args); without it the test would read the agentless
	// bucket and only pass on leakage from other tests' recordings
	// (verified: failed in isolation at every prior commit).
	args2, _ := json.Marshal(map[string]any{"request": "my working set", "agent_id": agent})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_meta","arguments":`+string(args2)+`}`))
	out2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error on workingset route: %s", out2)
	}
	if !strings.HasPrefix(out2, "[kern] working set") {
		t.Fatalf("expected the workingset listing header, got %q", out2)
	}
	if !strings.Contains(out2, "kern_compact_file") || !strings.Contains(out2, "etag=") {
		t.Fatalf("expected the recorded entry lines, got %q", out2)
	}
}

// TestConditionalFetchComposeStepsDoNotLeakEtag (HIGH-1): composed pipeline
// steps re-enter runTool under the SAME per-call scope. Each step must reset
// the scope's conditional-fetch state, so step 2 passing step 1's etag
// serves FULL text — never a stale "unchanged" verdict — and the outer
// kern_compose envelope (a NON-eligible tool) must not attach an inner
// step's etag/unchanged result fields.

// TestConditionalFetchEtagTiedToServeView (F-2): the etag is bound to the
// serve-time view (max_output budget) — a different serve view is different
// content, so a call with a different max_output must serve FULL text with a
// DIFFERENT etag even when the caller passes the previous call's etag, and
// the D1 cache keeps the two views as separate entries.
func TestConditionalFetchEtagTiedToServeView(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"root": root, "path": f}
	args1, _ := json.Marshal(base)
	resp1 := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args1)+`}`))
	if _, isErr := toolResultText(t, resp1); isErr {
		t.Fatalf("unexpected error: %+v", resp1)
	}
	e1 := resultField(t, resp1, "etag").(string)
	if e1 == "" {
		t.Fatal("first response must carry an etag")
	}

	// Same ask, different serve view, caller hands the previous etag: the
	// view difference means the content the caller WOULD get differs, so the
	// response must be the FULL text with a DIFFERENT etag.
	args2, _ := json.Marshal(map[string]any{"root": root, "path": f, "etag": e1, "max_output": "100"})
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compact_file","arguments":`+string(args2)+`}`))
	text2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error: %s", text2)
	}
	if strings.HasPrefix(text2, "unchanged (etag ") {
		t.Fatalf("a different serve view (max_output) must NOT be answered unchanged, got %q", text2)
	}
	e2 := resultField(t, resp2, "etag").(string)
	if e2 == e1 {
		t.Fatal("a different max_output view must mint a different etag")
	}

	// Two different-max_output calls with no etag both succeed — the D1 cache
	// keeps each serve view as its own entry (F-2 key semantics).
	args3, _ := json.Marshal(map[string]any{"root": root, "path": f, "max_output": "100"})
	resp3 := serveOne(t, writeReq("tools/call", 3, `{"name":"kern_compact_file","arguments":`+string(args3)+`}`))
	if _, isErr3 := toolResultText(t, resp3); isErr3 {
		t.Fatalf("unexpected error on second-view call: %+v", resp3)
	}
	if e3 := resultField(t, resp3, "etag").(string); e3 != e2 {
		t.Fatalf("same view must mint the same etag: got %q, want %q", e3, e2)
	}
}

// TestConditionalFetchComposeEnvelopeKeepsProvenanceOnLastStepShortCircuit
// (F-3): when the LAST inner composed step short-circuits (client etag
// matches), the outer kern_compose envelope must STILL carry the inner step's
// provenance — the unchanged flag belongs to the inner step, and the
// non-eligible outer tool must not inherit it when deciding provenance.

// TestConditionalFetchGovernedNeverShortCircuits (F-10 / HIGH-2): the
// governed/REST passthrough (CallToolGoverned) runs runTool with a bare ctx —
// no indexScope — so it cannot speak the conditional-fetch protocol. A caller
// that passes a matching etag must receive the FULL text, never the literal
// "unchanged (etag ...)" string as the entire output. Deleting the !hasScope
// guard in maybeShortCircuit must fail this test.
func TestConditionalFetchGovernedNeverShortCircuits(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Learn the real etag for this exact ask via a normal tools/call.
	args, _ := json.Marshal(map[string]any{"root": root, "path": f})
	resp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args)+`}`))
	if _, isErr := toolResultText(t, resp); isErr {
		t.Fatalf("unexpected error: %+v", resp)
	}
	e, _ := resultField(t, resp, "etag").(string)
	if e == "" {
		t.Fatal("eligible response must carry an etag")
	}

	// Drive the governed passthrough with a matching etag (no_cache forces a
	// fresh dispatch so the recompute path in maybeShortCircuit would
	// short-circuit without the scope guard).
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()
	out, err := s.CallToolGoverned(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f, "etag": e, "no_cache": "1"})
	if err != nil {
		t.Fatalf("governed call failed: %v", err)
	}
	if strings.HasPrefix(out, "unchanged (etag ") {
		t.Fatalf("the governed passthrough must never serve the unchanged short-circuit as the entire output, got %q", out)
	}
	if !strings.Contains(out, "foo") {
		t.Fatalf("the governed passthrough must serve the FULL text, got %q", out)
	}
}

// TestCallToolGovernedResultConditionalFetch (C2/F3): the structured
// governed path speaks the conditional-fetch protocol — a fresh serve
// carries the etag, a matching etag returns a structured unchanged verdict
// with the one-liner output, and a foreign etag re-serves the full text.
// The working-set record (trust-but-verify, C1/P1) is proven implicitly:
// the matching claim is accepted only because the fresh serve recorded it.
func TestCallToolGovernedResultConditionalFetch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()

	// Fresh serve: full text, etag minted, no unchanged verdict.
	r1, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f})
	if err != nil {
		t.Fatalf("governed result call failed: %v", err)
	}
	if !strings.Contains(r1.Output, "foo") || r1.Etag == "" || r1.Unchanged {
		t.Fatalf("fresh serve: want full text + etag + !unchanged, got %+v (output %.80q)", r1, r1.Output)
	}

	// Matching etag (no_cache forces the recompute path past D1): the
	// structured unchanged verdict — one-liner output, same etag.
	r2, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f, "etag": r1.Etag, "no_cache": "1"})
	if err != nil {
		t.Fatalf("governed result re-call failed: %v", err)
	}
	if !r2.Unchanged || r2.Etag != r1.Etag || !strings.Contains(r2.Output, "unchanged (etag ") {
		t.Fatalf("matching etag: want unchanged verdict + one-liner, got %+v (output %.80q)", r2, r2.Output)
	}

	// Foreign etag: full text re-serve, no unchanged verdict.
	r3, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", map[string]any{"root": root, "path": f, "etag": strings.Repeat("0", 64), "no_cache": "1"})
	if err != nil {
		t.Fatalf("governed result foreign-etag call failed: %v", err)
	}
	if r3.Unchanged || !strings.Contains(r3.Output, "foo") {
		t.Fatalf("foreign etag: want full text re-serve, got %+v (output %.80q)", r3, r3.Output)
	}
}

// TestMaybePreRenderShortCircuit (C2/F4, W6): a registry-verified etag claim
// skips the handler render when — and only when — the index identity and
// serve view are unchanged since the etag was minted. A foreign etag must
// force the full render, and so must a content change (file deleted — the
// probe-gated identity flips once past the freshness cooldown). File-backed
// tools that never load an index (kern_compact_file) record Identity "" and
// therefore never take the pre-render path — a documented safe degradation.
func TestMaybePreRenderShortCircuit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)
	defer s.Close()

	// Fresh serve with an INDEX-BACKED eligible tool: mints the etag,
	// records the working-set entry with the index identity and serve view
	// (the handler's loadIndex stamps scope.ix).
	exploreArgs := map[string]any{"root": root, "symbol": "foo"}
	r1, err := s.CallToolGovernedResult(context.Background(), "kern_explore", exploreArgs)
	if err != nil {
		t.Fatalf("governed result call failed: %v", err)
	}
	if r1.Etag == "" {
		t.Fatal("eligible response must carry an etag")
	}
	entry, ok := etag.Default.ServedEntry("", etag.CanonicalArgsKey(exploreArgs))
	if !ok {
		t.Fatal("fresh serve must record the working-set entry")
	}
	if entry.Identity == "" || entry.View == "" {
		t.Fatalf("recorded entry must carry identity+view for the pre-render gate, got %+v", entry)
	}

	ctx := context.WithValue(context.Background(), indexScopeKey{}, &indexScope{})

	// Registry-verified claim, unchanged identity+view: the one-liner
	// WITHOUT rendering.
	short := s.maybePreRenderShortCircuit(ctx, "kern_explore", map[string]any{"root": root, "symbol": "foo", "etag": r1.Etag})
	if short == "" || !strings.Contains(short, "unchanged (etag ") {
		t.Fatalf("verified claim with unchanged identity+view must pre-render short-circuit, got %q", short)
	}

	// Foreign etag: full render.
	short = s.maybePreRenderShortCircuit(ctx, "kern_explore", map[string]any{"root": root, "symbol": "foo", "etag": strings.Repeat("0", 64)})
	if short != "" {
		t.Fatalf("foreign etag must not pre-render short-circuit, got %q", short)
	}

	// Content change: delete the file, sleep past the freshness-probe
	// cooldown, then claim — the identity flips (probe-gated lookup returns
	// a different identity or "noindex"), so the full render must run even
	// though the registry entry still matches the claimed etag.
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5500 * time.Millisecond)
	short = s.maybePreRenderShortCircuit(ctx, "kern_explore", map[string]any{"root": root, "symbol": "foo", "etag": r1.Etag})
	if short != "" {
		t.Fatalf("changed identity must not pre-render short-circuit, got %q", short)
	}

	// File-backed eligible tool (kern_compact_file): no index load →
	// Identity "" → the pre-render gate never fires (safe degradation; the
	// post-render maybeShortCircuit still serves it). Rewrite the file
	// first (deleted above) so the serve succeeds.
	if err := os.WriteFile(f, []byte("package main\n\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfServe := map[string]any{"root": root, "path": f}
	if _, err := s.CallToolGovernedResult(context.Background(), "kern_compact_file", cfServe); err != nil {
		t.Fatalf("compact_file serve failed: %v", err)
	}
	cfEntry, ok := etag.Default.ServedEntry("", etag.CanonicalArgsKey(cfServe))
	if !ok {
		t.Fatal("compact_file serve must record a working-set entry")
	}
	if cfEntry.Identity != "" {
		t.Fatalf("file-backed tool with no index in scope must record Identity \"\" (no pre-render), got %q", cfEntry.Identity)
	}
}
