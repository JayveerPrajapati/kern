package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// hintExplore drives one kern_explore call through the server and returns its
// content text.
func hintExplore(t *testing.T, s *Server, id int, root, symbol string) string {
	t.Helper()
	pa, _ := json.Marshal(map[string]any{"name": "kern_explore", "arguments": map[string]any{"root": root, "symbol": symbol}})
	resp := s.toolCallResponse(json.RawMessage(fmt.Sprintf("\"%d\"", id)), pa).(map[string]any)
	if isErrorResult(resp) {
		t.Fatalf("kern_explore %q errored: %q", symbol, contentText(resp))
	}
	return contentText(resp)
}

const exploreHint = "next: kern_why names who depends on this; kern_impact before editing"

// TestHintsOnExploreSuccessOnce (B6): a successful kern_explore response
// carries the explore hint exactly once per session — the second explore
// call that session has no hint line.
func TestHintsOnExploreSuccessOnce(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	t1 := hintExplore(t, s, 1, root, "Greet")
	if !strings.Contains(t1, exploreHint) {
		t.Fatalf("the first explore response must carry the hint, got %q", t1)
	}
	t2 := hintExplore(t, s, 2, root, "helper")
	if strings.Contains(t2, exploreHint) {
		t.Fatalf("the hint must fire at most once per session, got %q", t2)
	}
}

// TestHintsDisabledEnv: KERN_HINTS=0 disables every hint.
func TestHintsDisabledEnv(t *testing.T) {
	t.Setenv("KERN_HINTS", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	for i := 1; i <= 2; i++ {
		txt := hintExplore(t, s, i, root, "Greet")
		if strings.Contains(txt, exploreHint) {
			t.Fatalf("disabled hints must not appear, got %q", txt)
		}
	}
}

// TestHintNeverOnLedgerPointer: a served-ledger pointer response never
// carries a hint line — even though the underlying call would otherwise have
// one.
func TestHintNeverOnLedgerPointer(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	t1 := hintExplore(t, s, 1, root, "Greet")
	if !strings.Contains(t1, exploreHint) {
		t.Fatalf("the first explore response must carry the hint, got %q", t1)
	}
	t2 := hintExplore(t, s, 2, root, "Greet")
	if !strings.HasPrefix(t2, "already served this session") {
		t.Fatalf("the exact repeat must be answered with the served-ledger pointer, got %q", t2)
	}
	if strings.Contains(t2, exploreHint) {
		t.Fatalf("the pointer response must never carry a hint, got %q", t2)
	}
}

// TestLoopMarkerSuppressesHint: when the Phase-1 loop-suspect marker is
// appended to a response, the hint is suppressed (max one advisory line per
// response — the marker already teaches).
func TestLoopMarkerSuppressesHint(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	// Three near-identical NO-MATCH kern_search queries: the first fires the
	// no-match hint, the third fires the loop marker — and must NOT also
	// carry the hint.
	const noMatchHint = "try kern_meta (NL router) or kern_buddy for orientation"
	queries := []string{
		"zzzz nonexistent symbol alpha",
		"zzzz nonexistent symbol alpha beta",
		"zzzz nonexistent symbol alpha gamma",
	}
	texts := make([]string, 3)
	for i, q := range queries {
		texts[i] = loopCall(t, s, i+1, root, q)
	}
	if !strings.Contains(texts[0], noMatchHint) {
		t.Fatalf("the first no-match response must carry the no-match hint, got %q", texts[0])
	}
	if !strings.Contains(texts[2], "[kern loop-suspect]") {
		t.Fatalf("the third near-duplicate must carry the loop marker, got %q", texts[2])
	}
	if strings.Contains(texts[2], noMatchHint) {
		t.Fatalf("the loop marker must suppress the hint on the same response, got %q", texts[2])
	}
}

// TestHintForSuppressedByLoopMarker pins the precedence at the wire-in level:
// a fresh session with the loop marker present gets NO hint (suppression, not
// once-fired dedup).
func TestHintForSuppressedByLoopMarker(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	// Fresh session, no marker: the hint fires.
	if line := s.hintFor(context.Background(), "kern_explore", map[string]any{"agent_id": "alice", "symbol": "foo"}, "some payload", ""); line == "" {
		t.Fatal("a fresh session without the loop marker must get the hint")
	}
	// Fresh session WITH the loop marker: suppressed.
	if line := s.hintFor(context.Background(), "kern_explore", map[string]any{"agent_id": "bob", "symbol": "foo"}, "some payload", "[kern loop-suspect] resembles call #1"); line != "" {
		t.Fatalf("the loop marker must suppress the hint even for a fresh session, got %q", line)
	}
}
