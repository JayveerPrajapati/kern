package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
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
	wantText := "unchanged (etag " + e1 + ") — identical to your earlier read; reuse your earlier copy. If this content is not in your context, call again without the etag argument to receive the full payload."
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

// exploreEtagAgentParams builds kern_explore tools/call params carrying an
// optional conditional-fetch etag and an optional caller agent_id.
func exploreEtagAgentParams(root, symbol, e, agent string) json.RawMessage {
	args := map[string]any{"root": root, "symbol": symbol}
	if e != "" {
		args["etag"] = e
	}
	if agent != "" {
		args["agent_id"] = agent
	}
	pa, _ := json.Marshal(map[string]any{"name": "kern_explore", "arguments": args})
	return pa
}

// TestEtagShortCircuitRequiresServedEntry pins the C1/P1 trust-but-verify
// gate: an unchanged short-circuit is granted only to an agent the
// working-set registry shows was actually handed that etag for those args.
// A different agent claiming the same etag (a subagent sharing a
// client-side cache with a different context) gets the full payload — and
// having NOW been served, its next claim with that etag short-circuits
// (the recovery path). An agent claiming an etag with no prior serve at
// all (the agentless bucket, first claim) is likewise rejected.
func TestEtagShortCircuitRequiresServedEntry(t *testing.T) {
	// Register the two test agents with the default agent's read permission
	// (the EnsureDefaultAgent shape): the governed retrieval family
	// authorizes explicit agent_ids through governance, and unknown agents
	// fail closed by design. Registration is in-memory and idempotent.
	if err := governance.RegisterAgent(governance.NewAgent("etag-agent-a", "Etag Test A", "default", []governance.Permission{
		{Resource: "context", Action: "read"},
	})); err != nil {
		t.Fatalf("register etag-agent-a: %v", err)
	}
	if err := governance.RegisterAgent(governance.NewAgent("etag-agent-b", "Etag Test B", "default", []governance.Permission{
		{Resource: "context", Action: "read"},
	})); err != nil {
		t.Fatalf("register etag-agent-b: %v", err)
	}
	root := mcpProject(t)
	s := cacheTestServer(t)
	defer s.Close()

	// (1) Agent A's first read: no etag, full payload, recorded under A.
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), exploreEtagAgentParams(root, "Greet", "", "etag-agent-a")).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("kern_explore errored: %q", contentText(r1))
	}
	e1 := resultEtag(r1)
	if e1 == "" {
		t.Fatal("first response must carry an etag")
	}
	if resultUnchanged(r1) {
		t.Fatal("first read must serve the full payload")
	}

	// (2) Agent A re-claims its own etag: registry hit -> short-circuit.
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), exploreEtagAgentParams(root, "Greet", e1, "etag-agent-a")).(map[string]any)
	if isErrorResult(r2) {
		t.Fatalf("agent-a re-claim errored: %q", contentText(r2))
	}
	if !resultUnchanged(r2) {
		t.Fatalf("a served agent re-claiming its etag must short-circuit, got: %+v", r2["result"])
	}

	// (3) Agent B claims A's etag for the same view: never served to B ->
	// full payload, no unchanged flag. The serve records B, by contract.
	r3 := s.toolCallResponse(json.RawMessage(`"3"`), exploreEtagAgentParams(root, "Greet", e1, "etag-agent-b")).(map[string]any)
	if isErrorResult(r3) {
		t.Fatalf("agent-b claim errored: %q", contentText(r3))
	}
	if resultUnchanged(r3) {
		t.Fatal("an agent claiming an etag it was never served must get the full payload")
	}
	if contentText(r3) == "" {
		t.Fatal("the rejected claim must still serve the full text")
	}

	// (4) Recovery: B, now served, re-claims the same etag -> short-circuit.
	r4 := s.toolCallResponse(json.RawMessage(`"4"`), exploreEtagAgentParams(root, "Greet", e1, "etag-agent-b")).(map[string]any)
	if isErrorResult(r4) {
		t.Fatalf("agent-b re-claim errored: %q", contentText(r4))
	}
	if !resultUnchanged(r4) {
		t.Fatalf("agent-b re-claim after being served must short-circuit, got: %+v", r4["result"])
	}

	// (5) The agentless bucket is independent: no agent_id, claiming the
	// etag without a prior agentless serve -> full payload.
	r5 := s.toolCallResponse(json.RawMessage(`"5"`), exploreEtagParams(root, "Greet", e1)).(map[string]any)
	if isErrorResult(r5) {
		t.Fatalf("agentless claim errored: %q", contentText(r5))
	}
	if resultUnchanged(r5) {
		t.Fatal("an agentless claim with no prior agentless serve must get the full payload")
	}
}

// TestEtagEligibleToolsHaveCatalogProperty pins the ADR-0012 wiring contract
// (C1/P4): every tool in etag.EligibleTools must exist in the catalog AND
// declare the optional `etag` string property in its InputSchema, and every
// catalog etag property must belong to an eligible tool — the two halves
// cannot drift apart.
func TestEtagEligibleToolsHaveCatalogProperty(t *testing.T) {
	if len(etag.EligibleTools) < 6 {
		t.Fatalf("expected at least 8 eligible tools, got %d", len(etag.EligibleTools))
	}
	for name := range etag.EligibleTools {
		var found *catalog.Tool
		for i := range catalog.All {
			if catalog.All[i].Name == name {
				found = &catalog.All[i]
				break
			}
		}
		if found == nil {
			t.Errorf("eligible tool %s missing from the catalog", name)
			continue
		}
		props, _ := found.InputSchema["properties"].(map[string]any)
		if _, ok := props["etag"]; !ok {
			t.Errorf("%s: eligible but its InputSchema lacks the etag property", name)
		}
	}
	for _, tool := range catalog.All {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if props == nil {
			continue
		}
		if _, has := props["etag"]; has && !etag.EligibleTools[tool.Name] {
			t.Errorf("%s: declares an etag property but is not in etag.EligibleTools", tool.Name)
		}
	}
}

// TestEtagContentScopedNotGenerationBound pins the content-scoping property
// behind ADR-0012's consistency model (C1/P5): the etag binds to the
// RESPONSE TEXT, not to the index generation or identity. An unrelated file
// edit that forces an index rebuild must NOT invalidate a view whose text is
// unchanged (a generation-bound etag would force a full re-serve of every
// view after any edit); editing the view's own content must flip the etag.
//
// The D1 response cache is disabled for this test (KERN_MCP_CACHE=0): a D1
// hit replays the stored response text and etag without running the handler,
// so the session's staleness probe never fires for same-args calls and the
// test would pin replay semantics instead of serve-path content-scoping.
// With D1 off, every call runs the handler, and loadIndex → Session.Index
// probes (rate-limited by the freshness cooldown: 1s without a native
// watcher, 5s with one) and rebuilds on detected edits. The sleeps sit past
// the longer bound so both host shapes take the same path.
func TestEtagContentScopedNotGenerationBound(t *testing.T) {
	t.Setenv("KERN_MCP_CACHE", "0")
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package fixture\n\nfunc Target() string { return \"v1\" }\n")
	write("b.go", "package fixture\n\nfunc Unrelated() int { return 1 }\n")

	s := cacheTestServer(t)
	defer s.Close()

	// (1) Baseline serve: etag over Target's view.
	r1 := s.toolCallResponse(json.RawMessage(`"1"`), exploreEtagParams(root, "Target", "")).(map[string]any)
	if isErrorResult(r1) {
		t.Fatalf("kern_explore errored: %q", contentText(r1))
	}
	e1 := resultEtag(r1)
	if e1 == "" {
		t.Fatal("first response carried no etag")
	}

	// Sleep past the freshness-probe cooldown (1s without a native watcher,
	// 5s with one) so the next call actually re-probes and rebuilds.
	time.Sleep(5500 * time.Millisecond)

	// (2) Unrelated edit: b.go grows a new symbol. The staleness probe
	// detects the changed tree and rebuilds the index (a NEW generation);
	// Target's view text is unchanged, so the old etag must still match —
	// content-scoped, not generation-bound.
	write("b.go", "package fixture\n\nfunc Unrelated() int { return 1 }\n\nfunc Extra() {}\n")
	r2 := s.toolCallResponse(json.RawMessage(`"2"`), exploreEtagParams(root, "Target", e1)).(map[string]any)
	if isErrorResult(r2) {
		t.Fatalf("kern_explore errored after unrelated edit: %q", contentText(r2))
	}
	if !resultUnchanged(r2) {
		t.Fatal("unrelated edit (index rebuilt, view text unchanged) must keep the etag matching")
	}
	// Rebuild proof: the symbol added to b.go resolves in the same post-edit
	// index, so the etag match above is genuine content-scoping, not a
	// stale-serve artifact.
	r2b := s.toolCallResponse(json.RawMessage(`"2b"`), exploreEtagParams(root, "Extra", "")).(map[string]any)
	if isErrorResult(r2b) {
		t.Fatalf("diagnostic: Extra did not resolve after the edit — the index never rebuilt: %q", contentText(r2b))
	}

	time.Sleep(5500 * time.Millisecond)

	// (3) Own-content edit: Target's body changes -> view text changes ->
	// the etag flips -> the stale etag must not match.
	write("a.go", "package fixture\n\nfunc Target() string { return \"v2 edited\" }\n")
	r3a := s.toolCallResponse(json.RawMessage(`"3a"`), exploreEtagParams(root, "Target", "")).(map[string]any)
	if isErrorResult(r3a) {
		t.Fatalf("kern_explore errored after own edit: %q", contentText(r3a))
	}
	if !strings.Contains(contentText(r3a), "v2 edited") {
		t.Fatalf("diagnostic: post-edit serve does not contain the edit — the index never rebuilt")
	}
	r3 := s.toolCallResponse(json.RawMessage(`"3"`), exploreEtagParams(root, "Target", e1)).(map[string]any)
	if isErrorResult(r3) {
		t.Fatalf("kern_explore errored after own edit: %q", contentText(r3))
	}
	if resultUnchanged(r3) {
		t.Fatal("editing the view's own content must flip the etag (stale etag must not match)")
	}
	if e3 := resultEtag(r3); e3 == "" {
		t.Fatal("re-serve after content change carried no etag")
	}
}
