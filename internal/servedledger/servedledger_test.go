package servedledger

import (
	"strconv"
	"strings"
	"testing"
)

// TestRecordLookupRoundtrip pins the core contract: a Record is found by
// Lookup with the one-line pointer carrying the call sequence number and the
// recorded etag, and re-recording the same key refreshes the etag and
// advances the sequence number.
func TestRecordLookupRoundtrip(t *testing.T) {
	l := New()
	if seq := l.Record("alice|t1", "kern_explore", `{"symbol":"foo"}`, "etagA", nil); seq != 1 {
		t.Fatalf("first record must return call #1, got %d", seq)
	}
	ptr, ok := l.Lookup("alice|t1", "kern_explore", `{"symbol":"foo"}`)
	if !ok {
		t.Fatal("lookup must hit after record")
	}
	want := "already served this session (call #1, etag etagA) — re-send with etag etagA to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
	if ptr != want {
		t.Fatalf("pointer = %q, want %q", ptr, want)
	}
	// Re-record refreshes the etag and advances the sequence number.
	if seq := l.Record("alice|t1", "kern_explore", `{"symbol":"foo"}`, "etagB", nil); seq != 2 {
		t.Fatalf("re-record must return call #2, got %d", seq)
	}
	ptr2, ok := l.Lookup("alice|t1", "kern_explore", `{"symbol":"foo"}`)
	if !ok {
		t.Fatal("lookup must still hit after re-record")
	}
	if !strings.Contains(ptr2, "call #2, etag etagB") {
		t.Fatalf("re-recorded pointer must carry the fresh sequence and etag, got %q", ptr2)
	}
}

// TestLookupMissOnUnserved: a never-recorded key misses.
func TestLookupMissOnUnserved(t *testing.T) {
	l := New()
	if _, ok := l.Lookup("default", "kern_search", `{"query":"x"}`); ok {
		t.Fatal("unserved key must miss")
	}
	// A different tool or different args under the same session must miss too.
	l.Record("default", "kern_search", `{"query":"x"}`, "e", nil)
	if _, ok := l.Lookup("default", "kern_search", `{"query":"y"}`); ok {
		t.Fatal("different args must miss")
	}
	if _, ok := l.Lookup("default", "kern_explore", `{"query":"x"}`); ok {
		t.Fatal("different tool must miss")
	}
}

// TestPointerOmitsEtagWhenNoneRecorded: the ", etag E" segment and the
// "re-send with etag E" clause disappear when no etag was recorded.
func TestPointerOmitsEtagWhenNoneRecorded(t *testing.T) {
	l := New()
	l.Record("default", "kern_search", `{"query":"x"}`, "", nil)
	ptr, ok := l.Lookup("default", "kern_search", `{"query":"x"}`)
	if !ok {
		t.Fatal("lookup must hit")
	}
	if strings.Contains(ptr, ", etag ") {
		t.Fatalf("no-etag pointer must omit the etag segment, got %q", ptr)
	}
	want := "already served this session (call #1) — re-send with the previous etag to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
	if ptr != want {
		t.Fatalf("pointer = %q, want %q", ptr, want)
	}
}

// TestFIFOEvictionAtCap: at MaxEntriesPerSession the oldest entry is
// evicted while the most recent stays.
func TestFIFOEvictionAtCap(t *testing.T) {
	l := New()
	const n = MaxEntriesPerSession + 10
	for i := 0; i < n; i++ {
		l.Record("default", "kern_explore", `{"symbol":"s`+strconv.Itoa(i)+`"}`, "e", nil)
	}
	if _, ok := l.Lookup("default", "kern_explore", `{"symbol":"s0"}`); ok {
		t.Fatal("oldest entry must be evicted at the FIFO cap")
	}
	if _, ok := l.Lookup("default", "kern_explore", `{"symbol":"s`+strconv.Itoa(n-1)+`"}`); !ok {
		t.Fatal("most recent entry must survive the FIFO cap")
	}
	if entries, _ := l.Summary("default"); entries != MaxEntriesPerSession {
		t.Fatalf("session must hold exactly the cap after eviction, got %d", entries)
	}
}

// TestPerSessionIsolation: two session keys never see each other's serves.
func TestPerSessionIsolation(t *testing.T) {
	l := New()
	l.Record("alice", "kern_explore", `{"symbol":"foo"}`, "e1", nil)
	if _, ok := l.Lookup("bob", "kern_explore", `{"symbol":"foo"}`); ok {
		t.Fatal("a different session key must not see another session's serve")
	}
	if _, ok := l.Lookup("alice", "kern_explore", `{"symbol":"foo"}`); !ok {
		t.Fatal("the recording session must still hit")
	}
}

// TestEnabledEnvSwitch: KERN_SERVED_LEDGER=0 disables; unset and "1" enable
// (default ON).
func TestEnabledEnvSwitch(t *testing.T) {
	t.Setenv("KERN_SERVED_LEDGER", "0")
	if Enabled() {
		t.Fatal("KERN_SERVED_LEDGER=0 must disable the ledger")
	}
	t.Setenv("KERN_SERVED_LEDGER", "")
	if !Enabled() {
		t.Fatal("unset KERN_SERVED_LEDGER must enable the ledger (default ON)")
	}
	t.Setenv("KERN_SERVED_LEDGER", "1")
	if !Enabled() {
		t.Fatal("KERN_SERVED_LEDGER=1 must enable the ledger")
	}
}

// TestResetClearsSession: Reset drops every serve for the session.
func TestResetClearsSession(t *testing.T) {
	l := New()
	l.Record("default", "kern_explore", `{"symbol":"foo"}`, "e", nil)
	l.Record("default", "kern_explore", `{"symbol":"bar"}`, "e", nil)
	l.Reset("default")
	if _, ok := l.Lookup("default", "kern_explore", `{"symbol":"foo"}`); ok {
		t.Fatal("Reset must clear the session's serves")
	}
	if entries, _ := l.Summary("default"); entries != 0 {
		t.Fatalf("session must be empty after Reset, got %d entries", entries)
	}
	// A different session is untouched.
	if _, ok := l.Lookup("other", "kern_explore", `{"symbol":"foo"}`); ok {
		t.Fatal("Reset of one session must not affect another")
	}
}

// TestSummaryCounts: entries counts one session's records; sessions counts
// every tracked session.
func TestSummaryCounts(t *testing.T) {
	l := New()
	l.Record("a", "kern_explore", `{"symbol":"x"}`, "e", nil)
	l.Record("a", "kern_explore", `{"symbol":"y"}`, "e", nil)
	l.Record("b", "kern_explore", `{"symbol":"x"}`, "e", nil)
	entries, sessions := l.Summary("a")
	if entries != 2 || sessions != 2 {
		t.Fatalf("Summary(a) = (%d, %d), want (2, 2)", entries, sessions)
	}
}

// TestFilesCoveredAggregatesSession (B3): FilesCovered aggregates the file
// references recorded across a session's entries — including re-recorded
// entries — and a second session's coverage is isolated.
func TestFilesCoveredAggregatesSession(t *testing.T) {
	l := New()
	l.Record("default", "kern_explore", `{"symbol":"a"}`, "e1", []string{"internal/mcp/server.go", "internal/domain/boundary.go"})
	l.Record("default", "kern_search", `{"query":"b"}`, "e2", []string{"internal/domain/boundary.go", "internal/mcp/toolpolicy.go"})
	l.Record("other", "kern_explore", `{"symbol":"c"}`, "e3", []string{"internal/agent/agent.go"})

	covered := l.FilesCovered("default")
	want := map[string]bool{
		"internal/mcp/server.go":      true,
		"internal/domain/boundary.go": true,
		"internal/mcp/toolpolicy.go":  true,
	}
	if len(covered) != len(want) {
		t.Fatalf("FilesCovered(default) = %v, want %d distinct files", covered, len(want))
	}
	for f := range want {
		if !covered[f] {
			t.Fatalf("FilesCovered(default) must include %q, got %v", f, covered)
		}
	}
	// A second session never sees the first session's coverage.
	if other := l.FilesCovered("other"); len(other) != 1 || !other["internal/agent/agent.go"] {
		t.Fatalf("FilesCovered(other) = %v, want only its own file", other)
	}
	if none := l.FilesCovered("unseen"); len(none) != 0 {
		t.Fatalf("FilesCovered(unseen) = %v, want empty", none)
	}
}
