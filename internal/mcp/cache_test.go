package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// cacheTestServer builds a server wired like the serveMany harness (unconfined
// roots, no gate) with a private cache dir, audit dir and a reset LRU, so each
// D1 cache test is fully isolated from the package-level cache state.
func cacheTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")
	lruReset()
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	s.gate = nil
	s.preTool = nil
	return s
}

// searchCall builds a tools/call params blob for kern_search on root.
func searchCall(root string, extra map[string]any) json.RawMessage {
	args := map[string]any{"root": root, "query": "Greet"}
	for k, v := range extra {
		args[k] = v
	}
	pa, _ := json.Marshal(map[string]any{"name": "kern_search", "arguments": args})
	return pa
}

// contentText extracts the first content text from a toolCallResponse result.
func contentText(resp map[string]any) string {
	res, ok := resp["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

// isErrorResult reports whether the response result carries isError=true.
func isErrorResult(resp map[string]any) bool {
	res, ok := resp["result"].(map[string]any)
	if !ok {
		return false
	}
	ie, _ := res["isError"].(bool)
	return ie
}

// toolCacheFiles lists mcp-toolcache-* entries currently on disk.
func toolCacheFiles(t *testing.T) []string {
	t.Helper()
	dir := cache.Path("data")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read cache data dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mcp-toolcache-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestToolCacheHitIdenticalToFreshRun: a cache hit returns the same raw text
// and byte-identical provenance as the fresh run that populated the entry
// (F6 correctness contract), including the provenance summary line modulo the
// live-derived "built Xs ago" age segment.
func TestToolCacheHitIdenticalToFreshRun(t *testing.T) {
	// The served ledger (B1) supersedes D1's full-payload replay for
	// same-session identical repeats of etag-eligible tools. This test pins
	// the D1 hit path itself (F6 provenance replay), so the session-dedup
	// layer is disabled to exercise it in isolation.
	t.Setenv("KERN_SERVED_LEDGER", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, nil)).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, nil)).(map[string]any)
	if isErrorResult(r1) || isErrorResult(r2) {
		t.Fatalf("search calls errored: %q / %q", contentText(r1), contentText(r2))
	}
	res1 := r1["result"].(map[string]any)
	res2 := r2["result"].(map[string]any)
	// Structured provenance (index evidence, verdict, symbols) is byte-identical.
	if !reflect.DeepEqual(res1["provenance"], res2["provenance"]) {
		t.Fatalf("provenance differs between fresh run and cache hit:\nfresh: %+v\nhit:   %+v", res1["provenance"], res2["provenance"])
	}
	// Content text identical except the live-derived segments: the age
	// seconds in the summary line ("built 3s ago" — recomputed at serve time
	// on both paths) and the threshold-gated duration footer ("[kern] took
	// Xs" — only a slow FRESH run crosses the threshold). The footer's
	// leading newline is part of the footer: normalizing it with the text
	// keeps both paths byte-equal whether or not the fresh run tripped the
	// threshold under full-suite load (F6, QA 2026-10-20).
	norm := regexp.MustCompile(`built \d+s ago|\n\[kern\] took \d+(?:\.\d+)?s`)
	t1 := norm.ReplaceAllString(contentText(r1), "")
	t2 := norm.ReplaceAllString(contentText(r2), "")
	if t1 != t2 {
		t.Fatalf("text differs between fresh run and cache hit:\nfresh: %q\nhit:   %q", t1, t2)
	}
	// Request-token metadata is recomputed from the same args → identical.
	md1, _ := res1["tokenMetadata"].(map[string]any)
	md2, _ := res2["tokenMetadata"].(map[string]any)
	if md1["tokensUsed"] != md2["tokensUsed"] {
		t.Fatalf("tokensUsed differs: %v vs %v", md1["tokensUsed"], md2["tokensUsed"])
	}
	// The hit is observable on disk + in the audit chain.
	if len(toolCacheFiles(t)) == 0 {
		t.Fatal("expected a cached entry on disk after the fresh run")
	}
}

// TestToolCacheProvenanceNormalizerAbsorbsFooterAsymmetry pins the
// full-suite F6 flake (2026-10-20): under load the FRESH index build
// crosses durationFooterThreshold and gains a "[kern] took Xs" footer,
// while the fast cache-hit replay does not, so the fresh text carries one
// extra separator newline. The summary normalization in
// TestToolCacheHitIdenticalToFreshRun must absorb the footer INCLUDING its
// leading newline (the footer's separator is part of the footer). This
// test forces that asymmetry deterministically by faking the slow-fresh
// footer and asserts the normalizer renders both paths byte-identical.
func TestToolCacheProvenanceNormalizerAbsorbsFooterAsymmetry(t *testing.T) {
	t.Setenv("KERN_SERVED_LEDGER", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, nil)).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, nil)).(map[string]any)
	if isErrorResult(r1) || isErrorResult(r2) {
		t.Fatalf("search calls errored: %q / %q", contentText(r1), contentText(r2))
	}
	// The observed full-suite failure: the fresh side gains the footer, the
	// hit side does not. Simulate the asymmetry (a local run is fast enough
	// that neither side emits a real footer).
	fresh := contentText(r1) + "\n[kern] took 99.0s"
	hit := contentText(r2)
	norm := regexp.MustCompile(`built \d+s ago|\n\[kern\] took \d+(?:\.\d+)?s`)
	t1 := norm.ReplaceAllString(fresh, "")
	t2 := norm.ReplaceAllString(hit, "")
	if t1 != t2 {
		t.Fatalf("normalizer must absorb the duration-footer asymmetry:\nfresh: %q\nhit:   %q", t1, t2)
	}
}

// TestD1LookupProbesFreshness (C2/F1, ADR-0012): on a watcher-less host
// nothing breaks the D1 replay cycle — the identity only rotates after a
// rebuild, and rebuilds only happen on handler runs (D1 misses) — so a
// same-args repeat call would replay the cached response even after the
// tree changed. The fix: cacheIndexIdentity validates the cached identity
// with the session's rate-limited staleness probe (Session.ProbeFresh)
// before trusting it; a stale probe invalidates the session cache (never
// builds), the identity reports "noindex", the lookup misses, and the
// handler serves rebuilt content. D1 is ON here (cacheTestServer default),
// so a regression re-introduces the replay and the stale text fails this
// test.
func TestD1LookupProbesFreshness(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package fixture\n\nfunc Target() string { return \"v1\" }\n")

	s := cacheTestServer(t)
	defer s.Close()

	// First call: builds the index, serves v1, stores the D1 entry.
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), exploreEtagParams(root, "Target", "")).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("kern_explore errored: %q", contentText(r1))
	}
	if !strings.Contains(contentText(r1), "v1") {
		t.Fatalf("first serve lacks v1: %q", contentText(r1))
	}

	// Sleep past the freshness cooldown so the probe re-arms (1s without a
	// native watcher, 5s with one — sleep past the longer bound).
	time.Sleep(5500 * time.Millisecond)

	// Edit the tree. The D1 lookup must now probe, find the cached index
	// stale, miss, and serve the rebuilt content — not the cached v1.
	write("a.go", "package fixture\n\nfunc Target() string { return \"v2 edited\" }\n")
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), exploreEtagParams(root, "Target", "")).(map[string]any)
	if isErrorResult(r2) {
		t.Fatalf("kern_explore errored after edit: %q", contentText(r2))
	}
	if !strings.Contains(contentText(r2), "v2 edited") {
		t.Fatalf("D1 replayed the stale response after an edit (probe not wired): %q", contentText(r2))
	}
}

// TestD1LookupBurstAfterCooldownNoStaleReplay (D1): a concurrent burst of
// cacheable calls that all arrive after the freshness cooldown expired must
// never replay stale content. The session's probe is single-flighted per
// cooldown window (project.ProbeFresh): one probe (the runner) invalidates
// the cached identity for the whole burst; every caller misses the stale
// entry and serves the rebuilt content. Before the memo, every burst caller
// ran its own full probe concurrently.
func TestD1LookupBurstAfterCooldownNoStaleReplay(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package fixture\n\nfunc Target() string { return \"v1\" }\n")

	s := cacheTestServer(t)
	defer s.Close()

	// Prime: builds the index and stores the v1 D1 entry.
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), exploreEtagParams(root, "Target", "")).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("prime kern_explore errored: %q", contentText(r1))
	}
	if !strings.Contains(contentText(r1), "v1") {
		t.Fatalf("prime serve lacks v1: %q", contentText(r1))
	}

	// Sleep past the freshness cooldown so the probe re-arms (1s without a
	// native watcher, 5s with one — sleep past the longer bound).
	time.Sleep(5500 * time.Millisecond)

	// Edit the tree, then fire the whole burst concurrently: every call
	// lands after the cooldown expired, so the burst shares one probe.
	write("a.go", "package fixture\n\nfunc Target() string { return \"v2 edited\" }\n")
	const burst = 6
	resps := make([]map[string]any, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			resps[n] = s.toolCallResponse(json.RawMessage(fmt.Sprintf("\"%d\"", n+2)), exploreEtagParams(root, "Target", "")).(map[string]any)
		}(i)
	}
	wg.Wait()
	for i, r := range resps {
		if isErrorResult(r) {
			t.Fatalf("burst call %d errored: %q", i, contentText(r))
		}
		if strings.Contains(contentText(r), "v1") {
			t.Fatalf("burst call %d replayed stale v1 after an edit: %q", i, contentText(r))
		}
		if !strings.Contains(contentText(r), "v2 edited") {
			t.Fatalf("burst call %d lacks v2 edited: %q", i, contentText(r))
		}
	}
}

// TestToolCacheKeyChangesWithIndexIdentity: the cache key embeds the index
// identity, so a different identity (an index rebuild) can never serve a stale
// entry (F2/F6). Different roots and different tools also key apart, while
// identity-only/conditional-fetch args (no_cache, agent_id, task) are stripped
// so identical answers share one entry (F8). max_output is NOT stripped (F-2):
// the etag is bound to the serve-time view, so different serve views are
// different content and key separate entries.
func TestToolCacheKeyChangesWithIndexIdentity(t *testing.T) {
	t.Parallel()
	args := map[string]any{"root": "/r", "query": "x"}
	kA := cacheKeyFor(context.Background(), "kern_search", args, "/r", "identityA")
	kB := cacheKeyFor(context.Background(), "kern_search", args, "/r", "identityB")
	if kA == kB {
		t.Fatal("key must change when the index identity differs (rebuild)")
	}
	kRoot := cacheKeyFor(context.Background(), "kern_search", args, "/other", "identityA")
	if kA == kRoot {
		t.Fatal("key must change when the resolved root differs")
	}
	kTool := cacheKeyFor(context.Background(), "kern_explore", args, "/r", "identityA")
	if kA == kTool {
		t.Fatal("key must change when the tool differs")
	}
	kStrip := cacheKeyFor(context.Background(), "kern_search", map[string]any{
		"root": "/r", "query": "x", "no_cache": "1", "agent_id": "a", "task": "t",
	}, "/r", "identityA")
	if kA != kStrip {
		t.Fatal("no_cache/agent_id/task must be stripped from the key (same entry)")
	}
	kView := cacheKeyFor(context.Background(), "kern_search", map[string]any{
		"root": "/r", "query": "x", "max_output": "500",
	}, "/r", "identityA")
	if kA == kView {
		t.Fatal("max_output must vary the key (the etag is view-bound, F-2)")
	}
}

// TestToolCacheMutatingToolNeverCached: stateful / mutating / git-diff-family
// tools are never cacheable (F1), and a call to one stores nothing.
func TestToolCacheMutatingToolNeverCached(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	for _, name := range []string{
		"kern_approve", "kern_register_host_sampler", "kern_lock",
		"kern_stats", "kern_changes", "kern_review", "kern_memory",
		"kern_doc", "kern_skill", "kern_snapshot",
	} {
		if toolCacheable(name) {
			t.Errorf("tool %s must never be cacheable", name)
		}
	}
	// Calling a non-cacheable tool through the full path stores nothing.
	pa, _ := json.Marshal(map[string]any{"name": "kern_stats", "arguments": map[string]any{"root": root}})
	s.toolCallResponse(json.RawMessage(`"1"`), pa)
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("non-cacheable tool wrote cache entries: %v", files)
	}
}

// TestToolCacheErrorResultNotCached: only successful (non-error) results are
// cached; an error result stores nothing (D1 correctness rule 1).
func TestToolCacheErrorResultNotCached(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{"name": "kern_search", "arguments": map[string]any{"root": root, "query": ""}})
	resp := s.toolCallResponse(json.RawMessage(`"1"`), pa).(map[string]any)
	if !isErrorResult(resp) {
		t.Fatalf("expected an isError result, got: %q", contentText(resp))
	}
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("error result was cached: %v", files)
	}
}

// TestToolCacheMaxOutputVarianceKeysSeparateEntries: max_output is part of
// the D1 key (F-2) — the etag is bound to the serve-time view, so different
// serve views are different content and key separate entries (the second call
// is a MISS, not a hit). Each call still applies its own budget at serve time
// (F8 serves the raw text; the budget truncates per call).
func TestToolCacheMaxOutputVarianceKeysSeparateEntries(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, map[string]any{"max_output": "10"})).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, map[string]any{"max_output": "100000"})).(map[string]any)
	// r2 is a MISS: its audit entry must NOT be tagged Policy:"tool-cache".
	entries := readAuditEntries(t)
	if len(entries) < 2 {
		t.Fatalf("expected two audit entries, got %d", len(entries))
	}
	if e := entries[len(entries)-1]; e["Policy"] == "tool-cache" {
		t.Fatalf("expected the second call (different max_output) to MISS the cache, entry: %+v", e)
	}
	// Per-call budgets: r1 truncated at 10 bytes, r2 served the full text.
	if !strings.Contains(contentText(r1), "[MCP output sandbox:") {
		t.Fatalf("expected r1 to be sandbox-truncated at max_output=10, got: %q", contentText(r1))
	}
	if strings.Contains(contentText(r2), "[MCP output sandbox:") {
		t.Fatalf("r2 must not be truncated (max_output=100000), got: %q", contentText(r2))
	}
}

// TestToolCacheNoCacheArgBypasses: per-call no_cache=1 bypasses lookup AND
// store — nothing is cached and no hit is recorded.
func TestToolCacheNoCacheArgBypasses(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, map[string]any{"no_cache": "1"}))
	s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, map[string]any{"no_cache": "1"}))
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("no_cache=1 calls wrote cache entries: %v", files)
	}
	for _, e := range readAuditEntries(t) {
		if e["Policy"] == "tool-cache" {
			t.Fatalf("no_cache=1 call recorded a cache hit: %+v", e)
		}
	}
}

// TestToolCacheDisabledByEnv: KERN_MCP_CACHE=0 disables the cache entirely —
// no lookup, no store.
func TestToolCacheDisabledByEnv(t *testing.T) {
	t.Setenv("KERN_MCP_CACHE", "0")
	if cacheEnabled() {
		t.Fatal("KERN_MCP_CACHE=0 must disable the cache")
	}
	root := mcpProject(t)
	s := cacheTestServer(t)
	s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, nil))
	s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, nil))
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("KERN_MCP_CACHE=0 wrote cache entries: %v", files)
	}
	for _, e := range readAuditEntries(t) {
		if e["Policy"] == "tool-cache" {
			t.Fatalf("KERN_MCP_CACHE=0 recorded a cache hit: %+v", e)
		}
	}
}

func TestToolCacheAuditEntryOnHit(t *testing.T) {
	// Same-session identical repeats are the served ledger's (B1) scenario —
	// it answers with the one-line pointer before D1 is consulted. This test
	// pins the D1 hit path's audit tagging (F5), so the session-dedup layer
	// is disabled to exercise it in isolation.
	t.Setenv("KERN_SERVED_LEDGER", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	s.toolCallResponse(json.RawMessage(`"1"`), searchCall(root, nil)) // miss → store
	s.toolCallResponse(json.RawMessage(`"2"`), searchCall(root, nil)) // hit
	entries := readAuditEntries(t)
	if len(entries) != 2 {
		t.Fatalf("expected two audit entries, got %d", len(entries))
	}
	e := entries[len(entries)-1]
	if e["Action"] != "tool_call" || e["Resource"] != "kern_search" {
		t.Fatalf("hit entry shape wrong: %+v", e)
	}
	if e["Result"] != "allowed" {
		t.Fatalf("hit entry Result = %v, want allowed (no new Result value)", e["Result"])
	}
	if e["Approved"] != true {
		t.Fatalf("hit entry Approved = %v, want true", e["Approved"])
	}
	if e["Policy"] != "tool-cache" || e["Reason"] != "served from cache" {
		t.Fatalf("hit entry must be tagged Policy=tool-cache Reason=served from cache: %+v", e)
	}
}

// TestToolCachePureArgToolNoIndex: pure-arg tools (kern_mask_pii) cache
// without any index involvement — no session is created and the "noindex"
// identity keys the entry.
func TestToolCachePureArgToolNoIndex(t *testing.T) {
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{
		"name": "kern_mask_pii", "arguments": map[string]any{"text": "hello bob@example.com"},
	})
	s.toolCallResponse(json.RawMessage(`"1"`), pa)
	s.toolCallResponse(json.RawMessage(`"2"`), pa)
	entries := readAuditEntries(t)
	if e := entries[len(entries)-1]; e["Policy"] != "tool-cache" {
		t.Fatalf("expected pure-arg tool hit, entry: %+v", e)
	}
	if s.sessCache.Len() != 0 {
		t.Fatalf("pure-arg tool must not create an index session, got %d", s.sessCache.Len())
	}
}

// TestToolCacheTTLExpiry: an entry older than KERN_MCP_CACHE_TTL is a miss and
// is deleted; a freshly stored entry hits again. Uses a pure-arg tool
// (kern_mask_pii) so no index/session/watcher state can interfere with the
// timing — the key stays "noindex" across the whole test.
func TestToolCacheTTLExpiry(t *testing.T) {
	t.Setenv("KERN_MCP_CACHE_TTL", "50ms")
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{
		"name": "kern_mask_pii", "arguments": map[string]any{"text": "hello bob@example.com"},
	})
	s.toolCallResponse(json.RawMessage(`"1"`), pa) // store
	time.Sleep(150 * time.Millisecond)             // age well past the TTL
	s.toolCallResponse(json.RawMessage(`"2"`), pa) // expired → miss
	entries := readAuditEntries(t)
	if e := entries[len(entries)-1]; e["Policy"] == "tool-cache" {
		t.Fatalf("expired entry must be a miss, entry: %+v", e)
	}
	s.toolCallResponse(json.RawMessage(`"3"`), pa) // fresh entry → hit
	entries = readAuditEntries(t)
	if e := entries[len(entries)-1]; e["Policy"] != "tool-cache" {
		t.Fatalf("fresh entry must hit, entry: %+v", e)
	}
}

// TestToolCacheStoredTextIsPiiMasked: the D1 cache masks response text
// BEFORE persisting, so an on-disk entry never carries raw secrets (e.g.
// source lines with API keys). A cache hit replays the masked form.
func TestToolCacheStoredTextIsPiiMasked(t *testing.T) {
	s := cacheTestServer(t)
	ctx := context.WithValue(context.Background(), indexScopeKey{}, &indexScope{})
	args := map[string]any{"root": "/tmp", "query": "q"}
	secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890ABCDEF"
	s.cacheStore(ctx, "kern_search", args, "deploy token "+secret+" now")

	key := toolCacheKeyPrefix + cacheKeyFor(context.Background(), "kern_search", args, "/tmp", "noindex")
	var e toolCacheEntry
	if err := cache.Load(key, &e); err != nil {
		t.Fatalf("entry must be stored: %v", err)
	}
	if strings.Contains(e.Text, secret) {
		t.Fatal("stored response text must be PII-masked")
	}
	if !strings.Contains(e.Text, "[MASKED_") {
		t.Fatalf("expected a masked placeholder in the stored text, got %q", e.Text)
	}
	// A lookup replays the masked form.
	got, ok := s.cacheLookup(context.Background(), "kern_search", args)
	if !ok {
		t.Fatal("entry must be served from the cache")
	}
	if got != e.Text {
		t.Fatalf("lookup text %q != stored text %q", got, e.Text)
	}
	if strings.Contains(got, secret) {
		t.Fatal("cache hit text must be masked")
	}
}

// TestToolCacheSizeCapSkipsLargeEntries (F9): entries whose raw text exceeds
// the 512 KiB cap are not stored.
func TestToolCacheSizeCapSkipsLargeEntries(t *testing.T) {
	s := cacheTestServer(t)
	ctx := context.WithValue(context.Background(), indexScopeKey{}, &indexScope{})
	args := map[string]any{"root": "/tmp", "query": "q"}
	key := toolCacheKeyPrefix + cacheKeyFor(context.Background(), "kern_search", args, "/tmp", "noindex")
	big := strings.Repeat("x", toolCacheEntrySizeCap+1)
	s.cacheStore(ctx, "kern_search", args, big)
	if cache.Exists(key) {
		t.Fatal("entry above the size cap must not be stored")
	}
	s.cacheStore(ctx, "kern_search", args, "small")
	if !cache.Exists(key) {
		t.Fatal("entry within the size cap must be stored")
	}
}

// TestCacheableAllowlistPinned pins the exact set of tools with
// Cacheable:true (F1) — mirroring risk_levels_test.go's structure. The vetted
// set: index-backed deterministic tools + pure-arg tools. Everything else —
// stateful, git-diff-family, docsearch, skill, note, snapshot — must stay
// uncacheable.
func TestCacheableAllowlistPinned(t *testing.T) {
	t.Parallel()
	cacheable := map[string]bool{
		// index-backed deterministic tools (F1) — the 2026-10-18 surface slim
		// shrank the catalog to 32 tools; the cacheable set is the 12 survivors
		// flagged Cacheable in the catalog.
		"kern_search": true, "kern_explore": true, "kern_context": true,
		"kern_compact_file": true, "kern_ast_search": true, "kern_why": true,
		"kern_path": true, "kern_arch": true, "kern_probe": true,
		"kern_graph": true, "kern_meta": true,
		// pure-arg tools (F1)
		"kern_mask_pii": true,
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = tool.Cacheable
	}
	for name, want := range cacheable {
		if !got[name] {
			t.Errorf("pinned cacheable tool %s is not registered", name)
			continue
		}
		if got[name] != want {
			t.Errorf("tool %s Cacheable = %v, want %v", name, got[name], want)
		}
	}
	for name, c := range got {
		if c && !cacheable[name] {
			t.Errorf("tool %s is Cacheable but not in the pinned allowlist", name)
		}
	}
	// The do-not-mark families stay excluded (F1/F3/F7). kern_repo_search is
	// excluded too (R2): multi-repo registry + other repos' indexes are not
	// covered by the current root's index identity.
	for _, name := range []string{
		"kern_lock", "kern_stats", "kern_health", "kern_flight", "kern_audit",
		"kern_agents", "kern_memory", "kern_runtime", "kern_stream", "kern_lsp_bridge",
		"kern_llm_providers", "kern_context_watch", "kern_agent", "kern_optimize",
		"kern_org", "kern_semantic",
		"kern_changes", "kern_review",
		"kern_doc", "kern_skill", "kern_snapshot",
		"kern_repo_search",
	} {
		if got[name] {
			t.Errorf("tool %s must not be cacheable", name)
		}
	}
}

// TestToolCacheMetaRoutingGate: kern_meta's own Cacheable flag is not enough —
// a kern_meta response is stored/served only when the sub-tool it routes to is
// itself cacheable (R1). The predicate resolves the routed tool from the
// request deterministically (the same classifyMetaRequest handleMeta uses), so
// the lookup gate (before dispatch) and the store gate (after dispatch) agree
// on the same verdict.
func TestToolCacheMetaRoutingGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		request string
		want    bool
	}{
		{"how does Greet work", true},      // → kern_explore (cacheable)
		{"show me the architecture", true}, // → kern_arch (cacheable)
		{"make a plan to refactor", false}, // → kern_plan (mutating)
		{"verify the refactor", false},     // → kern_verify (exec-gated)
		{"show my stats", false},           // → kern_stats (stateful)
		{"recall my memory", false},        // → kern_memory (stateful, uncacheable)
	}
	for _, tc := range cases {
		args := map[string]any{"request": tc.request}
		routed, _ := classifyMetaRequest(tc.request)
		if got := cacheableForCall(context.Background(), "kern_meta", args); got != tc.want {
			t.Errorf("kern_meta request %q (routes to %s): cacheableForCall = %v, want %v",
				tc.request, routed, got, tc.want)
		}
	}
}

// TestToolCacheMetaUncacheableRouteNeverCached: a kern_meta request routing to
// an uncacheable sub-tool (kern_stats) is never served from or stored to the
// D1 cache — the second identical call recomputes (no Policy:"tool-cache"
// audit entry, no mcp-toolcache-* entry on disk) (R1).
func TestToolCacheMetaUncacheableRouteNeverCached(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{
		"name":      "kern_meta",
		"arguments": map[string]any{"request": "show my stats", "root": root},
	})
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), pa).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), pa).(map[string]any)
	if isErrorResult(r1) || isErrorResult(r2) {
		t.Fatalf("meta calls errored: %q / %q", contentText(r1), contentText(r2))
	}
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("meta route to uncacheable sub-tool wrote cache entries: %v", files)
	}
	for _, e := range readAuditEntries(t) {
		if e["Policy"] == "tool-cache" {
			t.Fatalf("meta route to uncacheable sub-tool recorded a cache hit: %+v", e)
		}
	}
}

// TestToolCacheMetaCacheableRouteServedFromCache: a kern_meta request routing
// to a cacheable sub-tool (kern_explore) IS stored on the first call and
// served from the cache on the second — the hit is tagged
// Policy:"tool-cache" in the audit chain (R1).
func TestToolCacheMetaCacheableRouteServedFromCache(t *testing.T) {
	// Same-session identical repeats are the served ledger's (B1) scenario —
	// it answers with the one-line pointer before D1 is consulted. This test
	// pins the D1 hit path for a cacheable kern_meta route (R1), so the
	// session-dedup layer is disabled to exercise it in isolation.
	t.Setenv("KERN_SERVED_LEDGER", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{
		"name":      "kern_meta",
		"arguments": map[string]any{"request": "how does Greet work", "root": root},
	})
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), pa).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), pa).(map[string]any)
	if isErrorResult(r1) || isErrorResult(r2) {
		t.Fatalf("meta calls errored: %q / %q", contentText(r1), contentText(r2))
	}
	entries := readAuditEntries(t)
	if len(entries) < 2 {
		t.Fatalf("expected two audit entries, got %d", len(entries))
	}
	if e := entries[len(entries)-1]; e["Policy"] != "tool-cache" {
		t.Fatalf("expected the second meta call (cacheable route) to hit the cache, entry: %+v", e)
	}
}

// TestToolCacheSemanticSearchNeverCached (R3): kern_search with semantic=true
// is never stored or served — its results depend on KERN_EMBED_MODEL, which is
// not part of the cache key. Two identical semantic calls both recompute: no
// mcp-toolcache-* entry on disk and no Policy:"tool-cache" audit entry.
// KERN_LLM_PROVIDER=auto + an unreachable OLLAMA_HOST make the call succeed
// via the deterministic degradation path (embedding unavailable → ranked
// results), so the test exercises the cache gate rather than the error path.
func TestToolCacheSemanticSearchNeverCached(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "auto")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	root := mcpProject(t)
	s := cacheTestServer(t)
	pa, _ := json.Marshal(map[string]any{
		"name":      "kern_search",
		"arguments": map[string]any{"root": root, "query": "Greet", "semantic": "true"},
	})
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), pa).(map[string]any)
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), pa).(map[string]any)
	if isErrorResult(r1) || isErrorResult(r2) {
		t.Fatalf("semantic search calls errored: %q / %q", contentText(r1), contentText(r2))
	}
	if files := toolCacheFiles(t); len(files) != 0 {
		t.Fatalf("semantic=true calls wrote cache entries: %v", files)
	}
	for _, e := range readAuditEntries(t) {
		if e["Policy"] == "tool-cache" {
			t.Fatalf("semantic=true call recorded a cache hit: %+v", e)
		}
	}
	// The predicate itself: semantic args exclude a cacheable tool, and the
	// same tool without semantic stays cacheable.
	if cacheableForCall(context.Background(), "kern_search", map[string]any{"query": "Greet", "semantic": "true"}) {
		t.Fatal("kern_search with semantic=true must not be cacheable")
	}
	if !cacheableForCall(context.Background(), "kern_search", map[string]any{"query": "Greet"}) {
		t.Fatal("kern_search without semantic must remain cacheable")
	}
}

// TestToolCacheIdentityIgnoresBuiltAt (R4): BuiltAt is a build timestamp and
// must not participate in the cache identity — two identities with identical
// ContentRoot/TreeOID/GitCommit but different BuiltAt produce the SAME cache
// key (cross-rebuild/cross-process hits), while a content change still
// rotates the key.
func TestToolCacheIdentityIgnoresBuiltAt(t *testing.T) {
	t.Parallel()
	base := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	idA := &index.IndexIdentity{ContentRoot: "/r", TreeOID: "tree-abc", GitCommit: "c1", BuiltAt: base}
	idB := &index.IndexIdentity{ContentRoot: "/r", TreeOID: "tree-abc", GitCommit: "c1", BuiltAt: base.Add(24 * time.Hour)}
	args := map[string]any{"root": "/r", "query": "x"}
	kA := cacheKeyFor(context.Background(), "kern_search", args, "/r", indexIdentityString(idA))
	kB := cacheKeyFor(context.Background(), "kern_search", args, "/r", indexIdentityString(idB))
	if kA != kB {
		t.Fatal("identical ContentRoot/TreeOID/GitCommit with different BuiltAt must produce the same cache key")
	}
	// Content change still rotates the key (F2/F6).
	idC := &index.IndexIdentity{ContentRoot: "/r", TreeOID: "tree-xyz", GitCommit: "c1", BuiltAt: base}
	if kA == cacheKeyFor(context.Background(), "kern_search", args, "/r", indexIdentityString(idC)) {
		t.Fatal("a different TreeOID must produce a different cache key")
	}
}
