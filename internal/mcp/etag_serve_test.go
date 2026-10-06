package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
)

// exploreEtagParams builds a tools/call params blob for kern_explore on root,
// optionally carrying the conditional-fetch etag from a previous response.
func exploreEtagParams(root, symbol string, e string) json.RawMessage {
	args := map[string]any{"root": root, "symbol": symbol}
	if e != "" {
		args["etag"] = e
	}
	pa, _ := json.Marshal(map[string]any{"name": "kern_explore", "arguments": args})
	return pa
}

// resultEtag extracts result["etag"] from a toolCallResponse.
func resultEtag(resp map[string]any) string {
	res, ok := resp["result"].(map[string]any)
	if !ok {
		return ""
	}
	e, _ := res["etag"].(string)
	return e
}

// resultUnchanged extracts result["unchanged"] from a toolCallResponse.
func resultUnchanged(resp map[string]any) bool {
	res, ok := resp["result"].(map[string]any)
	if !ok {
		return false
	}
	u, _ := res["unchanged"].(bool)
	return u
}

// TestEtagConditionalFetchOnStdioPath pins the ADR-0012 conditional-fetch
// contract on the DEFAULT stdio loopback surface (the path opencode and
// plain MCP clients use): every eligible kern_explore/kern_context response
// carries an etag, and re-calling with that etag returns the tiny unchanged
// short-circuit instead of the full payload. This is the regression guard
// for the remediation: a missing etag on this path (or an ignored
// etag=<previous> arg) fails here.
func TestEtagConditionalFetchOnStdioPath(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	// (1) First call: no etag -> response MUST carry one.
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), exploreEtagParams(root, "Greet", "")).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("kern_explore errored: %q", contentText(r1))
	}
	e1 := resultEtag(r1)
	if e1 == "" {
		t.Fatal("kern_explore response must carry an etag on the stdio path")
	}
	if strings.Contains(contentText(r1), "unchanged (etag") {
		t.Fatalf("first call must serve the full payload, got short-circuit: %q", contentText(r1))
	}

	// (2) Re-call with that etag -> MUST be the unchanged path.
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), exploreEtagParams(root, "Greet", e1)).(map[string]any)
	if isErrorResult(r2) {
		t.Fatalf("kern_explore re-call errored: %q", contentText(r2))
	}
	if !resultUnchanged(r2) {
		t.Fatalf("re-call with a matching etag must set unchanged=true, got: %+v", r2["result"])
	}
	wantText := "unchanged (etag " + e1 + ")"
	if contentText(r2) != wantText {
		t.Fatalf("re-call must serve the unchanged short-circuit, got: %q", contentText(r2))
	}
	if resultEtag(r2) != e1 {
		t.Fatalf("unchanged response must echo the same etag, got %q want %q", resultEtag(r2), e1)
	}

	// (3) A STALE etag must serve the full fresh payload with the NEW etag.
	r3 := s.toolCallResponse(json.RawMessage(`"3"`), exploreEtagParams(root, "Greet", strings.Repeat("0", 64))).(map[string]any)
	if isErrorResult(r3) {
		t.Fatalf("kern_explore stale-etag call errored: %q", contentText(r3))
	}
	if resultUnchanged(r3) {
		t.Fatal("a stale etag must NOT short-circuit")
	}
	if strings.Contains(contentText(r3), "unchanged (etag") {
		t.Fatalf("stale etag must serve the full payload, got: %q", contentText(r3))
	}
	if resultEtag(r3) != e1 {
		t.Fatalf("stale-etag response must carry the fresh etag, got %q want %q", resultEtag(r3), e1)
	}

	// (4) kern_context participates in the same contract (the live evidence
	// covered both tools).
	c1 := s.toolCallResponse(json.RawMessage(`"4"`), func() json.RawMessage {
		pa, _ := json.Marshal(map[string]any{"name": "kern_context", "arguments": map[string]any{"root": root, "symbol": "Greet"}})
		return pa
	}()).(map[string]any)
	if isErrorResult(c1) {
		t.Fatalf("kern_context errored: %q", contentText(c1))
	}
	if resultEtag(c1) == "" {
		t.Fatal("kern_context response must carry an etag on the stdio path")
	}
}

// TestEtagWorkingSetRecorded pins the working-set registry side of the
// contract: an eligible response records (tool, args digest, etag) under the
// caller's agent bucket ("" -> the agentless "_" bucket), so kern_meta "my
// working set" can list it.
func TestEtagWorkingSetRecorded(t *testing.T) {
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	before := etag.Default.Count("")
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), func() json.RawMessage {
		pa, _ := json.Marshal(map[string]any{"name": "kern_explore", "arguments": map[string]any{"root": root, "symbol": "Greet"}})
		return pa
	}()).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("kern_explore errored: %q", contentText(r1))
	}
	if resultEtag(r1) == "" {
		t.Fatal("kern_explore response must carry an etag")
	}
	after := etag.Default.Count("")
	if after != before+1 {
		t.Fatalf("working set must record one entry per eligible response: before=%d after=%d", before, after)
	}
}
