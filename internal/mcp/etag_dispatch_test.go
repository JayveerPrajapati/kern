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
	// The served etag folds in the same effective budget the server used.
	budget, err := mcpserve.CallOutputBudget(map[string]any{"root": root, "path": f})
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
	if cacheKeyFor("kern_compact_file", base, "/repo", "noindex") != cacheKeyFor("kern_compact_file", withEtag, "/repo", "noindex") {
		t.Fatal("etag/identity args must not change the D1 cache key")
	}
	withMaxOutput := map[string]any{"root": "/repo", "path": "/repo/main.go", "max_output": "100"}
	if cacheKeyFor("kern_compact_file", base, "/repo", "noindex") == cacheKeyFor("kern_compact_file", withMaxOutput, "/repo", "noindex") {
		t.Fatal("max_output must change the D1 cache key (the etag is view-bound, F-2)")
	}
	if cacheKeyFor("kern_compact_file", base, "/repo", "noindex") == cacheKeyFor("kern_compact_file", map[string]any{"root": "/repo", "path": "/repo/other.go"}, "/repo", "noindex") {
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
	key := toolCacheKeyPrefix + cacheKeyFor("kern_compact_file", base, resolveRoot(root), "noindex")
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
	// Registry records under the agentless "_" bucket when no agent_id flows.
	args, _ := json.Marshal(map[string]any{"root": root, "path": f, "agent_id": "alice"})
	resp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_compact_file","arguments":`+string(args)+`}`))
	text1, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", text1)
	}
	e := resultField(t, resp, "etag").(string)

	entries := etag.Default.Entries("alice")
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
	if cacheableForCall("kern_meta", map[string]any{"request": "my working set"}) {
		t.Fatal("the workingset route must never be D1-cacheable")
	}

	// Full dispatch: kern_meta "my working set" renders the registry.
	resp2 := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_meta","arguments":{"request":"my working set"}}`))
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
func TestConditionalFetchComposeStepsDoNotLeakEtag(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc foo() string { return \"hi\" }\nfunc bar() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Learn step 1's etag from a standalone kern_context call: the compose
	// step hashes the same raw handler text over the same deterministic
	// index, so its step-1 etag equals this value.
	ctxArgs, _ := json.Marshal(map[string]any{"root": root, "symbol": "foo"})
	respCtx := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_context","arguments":`+string(ctxArgs)+`}`))
	if _, isErr := toolResultText(t, respCtx); isErr {
		t.Fatalf("unexpected error: %+v", respCtx)
	}
	e1, _ := resultField(t, respCtx, "etag").(string)
	if e1 == "" {
		t.Fatal("kern_context response must carry an etag")
	}

	// Compose: step 2 = kern_explore bar carrying etag=e1 (step 1's etag).
	// With the scope-leak bug, step 2 would reuse step 1's etag and wrongly
	// short-circuit to "unchanged"; it must serve the FULL explore text.
	pipeline, _ := json.Marshal([]map[string]any{
		{"tool": "kern_context", "args": map[string]any{"root": root, "symbol": "foo"}},
		{"tool": "kern_explore", "args": map[string]any{"root": root, "symbol": "bar", "etag": e1}},
	})
	compArgs, _ := json.Marshal(map[string]any{"root": root, "pipeline": string(pipeline)})
	respComp := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compose","arguments":`+string(compArgs)+`}`))
	out, isErr := toolResultText(t, respComp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.Contains(out, "unchanged (etag ") {
		t.Fatalf("compose step 2 must serve FULL text; got a stale unchanged short-circuit: %q", out)
	}
	if !strings.Contains(out, "bar") {
		t.Fatalf("expected step 2's explore content in the compose output, got %q", out)
	}
	// The outer compose envelope is NOT etag-eligible: it must never carry an
	// inner step's etag/unchanged fields.
	if resultField(t, respComp, "etag") != nil {
		t.Fatalf("non-eligible kern_compose envelope must not carry an etag: %+v", respComp)
	}
	if resultField(t, respComp, "unchanged") != nil {
		t.Fatalf("non-eligible kern_compose envelope must not carry unchanged: %+v", respComp)
	}
}

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
func TestConditionalFetchComposeEnvelopeKeepsProvenanceOnLastStepShortCircuit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc foo() string { return \"hi\" }\nfunc bar() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Learn the LAST step's real etag from a standalone kern_explore call so
	// the composed step's etag MATCHES and short-circuits.
	expArgs, _ := json.Marshal(map[string]any{"root": root, "symbol": "bar"})
	respExp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_explore","arguments":`+string(expArgs)+`}`))
	if _, isErr := toolResultText(t, respExp); isErr {
		t.Fatalf("unexpected error: %+v", respExp)
	}
	e2, _ := resultField(t, respExp, "etag").(string)
	if e2 == "" {
		t.Fatal("kern_explore response must carry an etag")
	}

	pipeline, _ := json.Marshal([]map[string]any{
		{"tool": "kern_context", "args": map[string]any{"root": root, "symbol": "foo"}},
		{"tool": "kern_explore", "args": map[string]any{"root": root, "symbol": "bar", "etag": e2}},
	})
	compArgs, _ := json.Marshal(map[string]any{"root": root, "pipeline": string(pipeline)})
	respComp := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_compose","arguments":`+string(compArgs)+`}`))
	out, isErr := toolResultText(t, respComp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	// The last inner step short-circuits, so its unchanged text appears in the
	// envelope — but the envelope must STILL carry the provenance field and
	// the summary line (F-3).
	if !strings.Contains(out, "unchanged (etag ") {
		t.Fatalf("expected the last step to short-circuit inside the compose envelope, got %q", out)
	}
	if resultField(t, respComp, "provenance") == nil {
		t.Fatalf("the compose envelope must carry provenance even when the last inner step short-circuits: %+v", respComp)
	}
	if !strings.Contains(out, "[kern] index:") {
		t.Fatalf("expected the provenance summary line in the envelope text, got %q", out)
	}
}

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
