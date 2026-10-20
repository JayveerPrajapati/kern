package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// loopCall drives one kern_search call through the given server (sequential,
// same session) and returns its content text.
func loopCall(t *testing.T, s *Server, id int, root, query string) string {
	t.Helper()
	resp := s.toolCallResponse(json.RawMessage(fmt.Sprintf("\"%d\"", id)), searchCall(root, map[string]any{"query": query})).(map[string]any)
	if isErrorResult(resp) {
		t.Fatalf("kern_search %q errored: %q", query, contentText(resp))
	}
	return contentText(resp)
}

// TestLoopSuspectMarkerOnThirdNearDuplicate (B4): three near-identical
// kern_search queries in one session — the marker appears exactly once, on
// the THIRD call (the second is only 1 call back and is excluded by the
// >= 2-calls-back rule), with the exact advisory line referencing the first
// call.
func TestLoopSuspectMarkerOnThirdNearDuplicate(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	q1 := "Greet dispatch work flow internals"
	q2 := "Greet dispatch work flow internals deeply"
	q3 := "Greet dispatch work flow internals review"

	t1 := loopCall(t, s, 1, root, q1)
	t2 := loopCall(t, s, 2, root, q2)
	t3 := loopCall(t, s, 3, root, q3)

	for i, txt := range []string{t1, t2} {
		if strings.Contains(txt, "[kern loop-suspect]") {
			t.Fatalf("call %d must not carry the marker, got %q", i+1, txt)
		}
	}
	want := "[kern loop-suspect] resembles call #1 (sim 0.88) — you may already have this; re-use etag or emit a plan (kern_plan)"
	if !strings.Contains(t3, want) {
		t.Fatalf("third call must carry the exact marker line %q, got %q", want, t3)
	}
	if strings.Count(t1+t2+t3, "[kern loop-suspect]") != 1 {
		t.Fatalf("the marker must appear exactly once across the three calls, got %d", strings.Count(t1+t2+t3, "[kern loop-suspect]"))
	}
}

// TestLoopDetectDistinctQueriesNoMarker: unrelated queries never flag.
func TestLoopDetectDistinctQueriesNoMarker(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	for i, q := range []string{"Greet dispatch work flow", "helper returns value", "main entry point"} {
		txt := loopCall(t, s, i+1, root, q)
		if strings.Contains(txt, "[kern loop-suspect]") {
			t.Fatalf("distinct query %q must not carry the marker, got %q", q, txt)
		}
	}
}

// TestLoopDetectDisabledEnvNoMarker: KERN_LOOP_DETECT=0 turns the detector
// off — near-identical repeats carry no marker.
func TestLoopDetectDisabledEnvNoMarker(t *testing.T) {
	t.Setenv("KERN_LOOP_DETECT", "0")
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	for i, q := range []string{
		"Greet dispatch work flow internals",
		"Greet dispatch work flow internals deeply",
		"Greet dispatch work flow internals review",
	} {
		txt := loopCall(t, s, i+1, root, q)
		if strings.Contains(txt, "[kern loop-suspect]") {
			t.Fatalf("disabled detector must not emit the marker, got %q", txt)
		}
	}
}

// TestLoopDetectNoMarkerOnLedgerPointer: the marker is appended only to
// full-payload responses. A call answered by the served-ledger pointer must
// stay byte-identical to the pointer — even when the loop detector would
// have flagged it.
func TestLoopDetectNoMarkerOnLedgerPointer(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	q1 := "Greet dispatch work flow internals"
	q2 := "Greet dispatch work flow internals review"

	t1 := loopCall(t, s, 1, root, q1) // full payload, ring #1
	if strings.Contains(t1, "[kern loop-suspect]") {
		t.Fatalf("first call must not carry the marker, got %q", t1)
	}

	// Exact repeat → served-ledger pointer; the ring still records it (#2).
	t2 := loopCall(t, s, 2, root, q1)
	if !strings.HasPrefix(t2, "already served this session (call #1") {
		t.Fatalf("exact repeat must be answered with the served-ledger pointer, got %q", t2)
	}
	if strings.Contains(t2, "[kern loop-suspect]") {
		t.Fatalf("the pointer response must not carry the marker, got %q", t2)
	}

	// Near-duplicate → full payload WITH the marker (proves the detector
	// machinery is live).
	t3 := loopCall(t, s, 3, root, q2)
	if !strings.Contains(t3, "[kern loop-suspect] resembles call #1 (sim 0.88)") {
		t.Fatalf("near-duplicate must carry the marker on the full payload, got %q", t3)
	}

	// Exact repeat again → pointer again. The loop detector would flag the
	// earlier q1 (call #2, 2+ calls back), but the pointer response must not
	// carry the marker.
	t4 := loopCall(t, s, 4, root, q1)
	if !strings.HasPrefix(t4, "already served this session") {
		t.Fatalf("fourth call must be answered with the served-ledger pointer, got %q", t4)
	}
	if strings.Contains(t4, "[kern loop-suspect]") {
		t.Fatalf("the pointer response must never carry the marker, got %q", t4)
	}
}
