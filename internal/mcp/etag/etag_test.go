package etag

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHashStability locks the etag contract: identical text + identical
// schema versions mint identical etags; any change to the text, the tool's
// schema version or the scheme version mints a different one. The hash is
// sha256 hex of the raw pre-sandbox text (no per-file hashing), 64 chars.
func TestHashStability(t *testing.T) {
	text := "# resolved greet -> internal/greet.go:12\nfunc greet() string { return \"hi\" }\n"
	e1 := Hash(text, "1.0.0")
	e2 := Hash(text, "1.0.0")
	if e1 != e2 {
		t.Fatalf("identical input must mint identical etags: %q vs %q", e1, e2)
	}
	if len(e1) != 64 {
		t.Fatalf("etag must be sha256 hex (64 chars), got %d: %q", len(e1), e1)
	}
	if Hash(text+"\n", "1.0.0") == e1 {
		t.Fatal("changed text must mint a different etag")
	}
	if Hash(text, "1.0.1") == e1 {
		t.Fatal("changed tool schema version must mint a different etag")
	}
	e1Repeat := Hash(text, "1.0.0")
	if e1Repeat != e1 {
		t.Fatal("etag must remain stable under repeated calls")
	}
	// The tool schema version and the scheme version both participate.
	if strings.Contains(e1, "\x00") {
		t.Fatal("etag must be hex-encoded, no raw separators")
	}
}

// TestUnchangedResponseText pins the short-circuit body (ADR-0012 item 2).
func TestUnchangedResponseText(t *testing.T) {
	e := Hash("same", "1.0.0")
	want := "unchanged (etag " + e + ")"
	if got := UnchangedResponseText(e); got != want {
		t.Fatalf("UnchangedResponseText = %q, want %q", got, want)
	}
}

// TestCanonicalArgsKeyStripsServeTimeArgs: the registry key must ignore the
// etag itself plus no_cache/agent_id/task (the F8 identity set), so an
// identical ask keys one entry across those variances. max_output is the
// exception (F-2): the etag is bound to the serve-time view, so a different
// serve view is different content and keys its own registry entry. Any other
// arg difference (root, symbol, lines) changes the key.
func TestCanonicalArgsKeyStripsServeTimeArgs(t *testing.T) {
	base := map[string]any{"symbol": "greet", "root": "/repo", "lines": "12"}
	withEtag := map[string]any{"symbol": "greet", "root": "/repo", "lines": "12", "etag": "deadbeef", "no_cache": "1", "agent_id": "alice", "task": "t1"}
	if CanonicalArgsKey(base) != CanonicalArgsKey(withEtag) {
		t.Fatal("identity/conditional-fetch args must not change the registry key")
	}
	if CanonicalArgsKey(base) == CanonicalArgsKey(map[string]any{"symbol": "greet", "root": "/repo", "lines": "12", "max_output": "100"}) {
		t.Fatal("max_output must change the registry key (the etag is view-bound, F-2)")
	}
	if CanonicalArgsKey(base) == CanonicalArgsKey(map[string]any{"symbol": "greet", "root": "/repo", "lines": "13"}) {
		t.Fatal("a real arg difference must change the registry key")
	}
}

// TestHashViewFoldsServeView: the serve-time view (max_output budget) is part
// of the etag input (F-2) — two calls over byte-identical text with different
// views mint different etags, while Hash (the view-agnostic CLI form) equals
// HashView with an empty view.
func TestHashViewFoldsServeView(t *testing.T) {
	text := "# resolved greet -> internal/greet.go:12\nfunc greet() string { return \"hi\" }\n"
	eDefault := HashView(text, "1.0.0", "24576")
	if eDefault == HashView(text, "1.0.0", "100") {
		t.Fatal("a different serve view must mint a different etag")
	}
	eView1 := HashView(text, "1.0.0", "100")
	eView2 := HashView(text, "1.0.0", "100")
	if eView1 != eView2 {
		t.Fatal("identical text + view must stay stable")
	}
	if Hash(text, "1.0.0") != HashView(text, "1.0.0", "") {
		t.Fatal("Hash must equal HashView with an empty view (CLI semantics unchanged)")
	}
}

// TestRegistryLRUBounds exercises both bounds: per-agent eviction at
// MaxEntriesPerAgent and cross-agent eviction at MaxAgents. The MRU entry
// survives each eviction; the LRU tail is dropped.
func TestRegistryLRUBounds(t *testing.T) {
	r := NewRegistry()

	// Per-agent bound: record more than MaxEntriesPerAgent distinct args.
	keep := Entry{Tool: "kern_context", Args: "first", ETag: "e-first", Stamp: time.Now()}
	r.Record("alice", keep)
	for i := 0; i < MaxEntriesPerAgent; i++ {
		r.Record("alice", Entry{Tool: "kern_context", Args: fmt.Sprintf("arg-%d", i), ETag: "e", Stamp: time.Now()})
	}
	if got := r.Count("alice"); got > MaxEntriesPerAgent {
		t.Fatalf("agent working set grew past %d: %d", MaxEntriesPerAgent, got)
	}
	// "first" was recorded first and not re-touched → evicted; re-touching
	// it would promote it to MRU and evict the tail instead.
	if len(r.Entries("alice")) == 0 {
		t.Fatal("working set must retain the newest entries")
	}
	if r.Count("alice") != MaxEntriesPerAgent {
		t.Fatalf("working set should sit exactly at the bound, got %d", r.Count("alice"))
	}

	// Cross-agent bound: record for more than MaxAgents distinct agents.
	r2 := NewRegistry()
	for i := 0; i < MaxAgents+16; i++ {
		r2.Record(fmt.Sprintf("agent-%02d", i), Entry{Tool: "kern_retrieve", Args: "x", ETag: "e", Stamp: time.Now()})
	}
	if got := r2.AgentCount(); got > MaxAgents {
		t.Fatalf("registry tracked more than %d agents: %d", MaxAgents, got)
	}
	if r2.AgentCount() != MaxAgents {
		t.Fatalf("agent count should sit exactly at the bound, got %d", r2.AgentCount())
	}

	// MRU semantics (LOW-6): an agent that records again after other agents
	// pushed toward the bound must be PROMOTED — eviction picks the
	// longest-IDLE agent, not the longest-created one.
	r3 := NewRegistry()
	for i := 0; i < MaxAgents; i++ {
		r3.Record(fmt.Sprintf("agent-%02d", i), Entry{Tool: "kern_retrieve", Args: "x", ETag: "e", Stamp: time.Now()})
	}
	// agent-00 is the LRU tail (recorded first, never re-touched). Recording
	// it again promotes it to MRU front.
	r3.Record("agent-00", Entry{Tool: "kern_context", Args: "re-touch", ETag: "e2", Stamp: time.Now()})
	if _, ok := r3.byAgent["agent-00"]; !ok {
		t.Fatal("re-recorded agent must still be tracked")
	}
	// One more agent pushes past the bound: agent-01 (now the LRU tail) is
	// evicted; the re-recorded agent-00 survives.
	r3.Record("overflow", Entry{Tool: "kern_retrieve", Args: "x", ETag: "e", Stamp: time.Now()})
	if r3.AgentCount() != MaxAgents {
		t.Fatalf("agent count should stay exactly at the bound, got %d", r3.AgentCount())
	}
	if _, ok := r3.byAgent["agent-01"]; ok {
		t.Fatal("the longest-idle agent (agent-01) must be evicted, not the re-recorded one")
	}
	if _, ok := r3.byAgent["agent-00"]; !ok {
		t.Fatal("the re-recorded agent-00 must survive the eviction (true MRU semantics)")
	}
}

// TestRegistryConcurrencySafety hammers Record/Entries/Count from many
// goroutines (run with -race) to prove the mutex guard holds and the bounds
// never overflow under contention.
func TestRegistryConcurrencySafety(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			agent := fmt.Sprintf("agent-%02d", g)
			for i := 0; i < 200; i++ {
				r.Record(agent, Entry{Tool: "kern_explore", Args: fmt.Sprintf("arg-%d", i%8), ETag: "e", Stamp: time.Now()})
				_ = r.Entries(agent)
				_ = r.Count(agent)
				_ = r.AgentCount()
			}
		}(g)
	}
	wg.Wait()
	if r.AgentCount() > MaxAgents {
		t.Fatalf("concurrent records exceeded the agent bound: %d", r.AgentCount())
	}
	for g := 0; g < 16; g++ {
		if c := r.Count(fmt.Sprintf("agent-%02d", g)); c > MaxEntriesPerAgent {
			t.Fatalf("concurrent records exceeded the per-agent bound: %d", c)
		}
	}
}

// TestAgentlessBucket: calls without an agent_id land under "_" so
// un-attributed reads still populate the working set (ADR-0012 item 5).
func TestAgentlessBucket(t *testing.T) {
	r := NewRegistry()
	r.Record("", Entry{Tool: "kern_context", Args: "a", ETag: "e", Stamp: time.Now()})
	r.Record("alice", Entry{Tool: "kern_context", Args: "a", ETag: "e", Stamp: time.Now()})
	if r.Count("") != 1 {
		t.Fatalf("agentless reads must be tracked under the _ bucket, got %d", r.Count(""))
	}
	if r.Count("alice") != 1 {
		t.Fatalf("attributed reads must be tracked under their agent, got %d", r.Count("alice"))
	}
	_ = r.Render("")
	_ = r.Render("alice")
	_ = r.Render("nobody")
}

// TestRenderWorkingsetFormat pins the listing shape (the contract B2 will
// spec the plugin against): one header line, then one line per entry with
// tool, args=, etag=, ts=.
func TestRenderWorkingsetFormat(t *testing.T) {
	r := NewRegistry()
	r.Record("bob", Entry{Tool: "kern_compact_file", Args: "abcd", ETag: "e1", Stamp: time.Unix(1700000000, 0).UTC()})
	r.Record("bob", Entry{Tool: "kern_context", Args: "ef01", ETag: "e2", Stamp: time.Unix(1700000001, 0).UTC()})
	out := r.Render("bob")
	if !strings.HasPrefix(out, "[kern] working set (2 entries):\n") {
		t.Fatalf("bad header: %q", out)
	}
	if !strings.Contains(out, "kern_context args=ef01 etag=e2 ts=2023-11-14T22:13:21Z") {
		t.Fatalf("expected newest-first entry line, got: %q", out)
	}
	if !strings.Contains(out, "kern_compact_file args=abcd etag=e1") {
		t.Fatalf("expected the older entry line, got: %q", out)
	}
	if iCtx := strings.Index(out, "kern_context"); iCtx < 0 || iCtx > strings.Index(out, "kern_compact_file") {
		t.Fatalf("entries must be MRU first, got: %q", out)
	}
}
