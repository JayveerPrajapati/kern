package mcp

// End-to-end JSON-RPC tool tests (Finding 10): real kern_* tool calls POSTed
// through the server's HTTP transport (ServeHTTP via httptest). Unlike the
// protocol-only tests in http_test.go, these exercise the full
// dispatch→allowlist→schema-negotiation→arg-validation→handler→leaf path for
// representative tools across the catalog: symbol search, graph/explore, the
// call-graph neighbourhood, the impact/risk analysis surface, and the
// kern_meta NL router. Each test asserts the JSON-RPC result shape (no rpc
// error, expected content, provenance where the tool is index-backed) plus
// the failure paths (isError tool results carrying the exact cause).
// Coverage note (2026-11-20 remediation): kern_retrieve/kern_what_if were
// removed from dispatch in the v0.9.11 surface slim, so their intended e2e
// cases live on kern_graph and kern_impact — the served surfaces with the
// matching contracts (structural render + unknown-symbol failure).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// e2eCall POSTs a real tools/call over HTTP and returns the decoded JSON-RPC
// response, failing the test on transport status or rpc-level errors. It
// reuses doHTTP (http_test.go) so the exact production ServeHTTP path runs.
func e2eCall(t *testing.T, s *Server, id int, name string, args map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":` + jsonID(id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + string(raw) + `}}`
	rr := doHTTP(t, s, http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d: %s", name, rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: unmarshal: %v (raw: %s)", name, err, rr.Body.String())
	}
	if e, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("%s: rpc error: %v", name, e)
	}
	return resp
}

// e2eText extracts the tool result text, failing on isError results.
func e2eText(t *testing.T, name string, resp map[string]any) string {
	t.Helper()
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("%s: isError result: %s", name, text)
	}
	return text
}

// e2eProvenance returns the structured provenance object of a result, or nil.
func e2eProvenance(resp map[string]any) map[string]any {
	res, _ := resp["result"].(map[string]any)
	prov, _ := res["provenance"].(map[string]any)
	return prov
}

// e2eAssertion is the declarative content contract for an e2e tool result
// (E2E remediation Phase 1): MustContain lists substrings the result text
// must include, MustNotContain substrings that must never appear. Asserting
// content this way replaces shallow single-strings.Contains checks with
// structure- and content-aware validation that survives formatting churn.
type e2eAssertion struct {
	MustContain    []string
	MustNotContain []string
}

// assertE2EResult validates a successful (non-isError) tool result against
// check: the text must be nonempty, contain every MustContain substring and
// no MustNotContain substring. It fails the test on rpc errors or isError
// results, so a happy-path case can never silently pass on a failure shape.
func assertE2EResult(t *testing.T, name string, resp map[string]any, check e2eAssertion) {
	t.Helper()
	text := e2eText(t, name, resp)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("%s: result text is empty", name)
	}
	for _, want := range check.MustContain {
		if !strings.Contains(text, want) {
			t.Errorf("%s: result missing %q:\n%s", name, want, text)
		}
	}
	for _, banned := range check.MustNotContain {
		if strings.Contains(text, banned) {
			t.Errorf("%s: result must not contain %q:\n%s", name, banned, text)
		}
	}
}

// assertE2EToolError validates a failure-path tool result over the same HTTP
// transport: the handler error must surface as an isError TOOL RESULT (the
// D6 contract — handler-level execution errors are never JSON-RPC protocol
// errors, which stay reserved for unknown/empty tool names) whose text
// carries every wanted substring, so failure paths are pinned just as
// tightly as happy paths.
func assertE2EToolError(t *testing.T, name string, resp map[string]any, want ...string) string {
	t.Helper()
	if e, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("%s: handler error must surface as isError tool result, not JSON-RPC error: %v", name, e)
	}
	text, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("%s: expected isError result, got success:\n%s", name, text)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatalf("%s: isError result carries no message", name)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s: error text missing %q: %s", name, w, text)
		}
	}
	return text
}

// TestE2ESearch drives kern_search over HTTP on the tiny testfixture repo:
// dispatch → allowlist → arg validation → handler → index leaf. The search
// result must carry the index-backed provenance stamp.
func TestE2ESearch(t *testing.T) {
	root := fixtureRoot(t)
	// The workspace (KERN_ROOTS) and the confinement gate (KERN_MCP_ROOTS)
	// must both include the temp fixture; the gate fails closed to the
	// process cwd otherwise.
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 1, "kern_search", map[string]any{"root": root, "query": "main", "limit": 1})
	text := e2eText(t, "kern_search", resp)
	if !strings.Contains(text, "main") {
		t.Fatalf("search for %q did not return the main symbol: %s", "main", text)
	}
	prov := e2eProvenance(resp)
	if prov == nil {
		t.Fatalf("kern_search is index-backed and must carry structured provenance: %s", text)
	}
	// The one-line text stamp prints ONLY when the index state is unusual
	// (stale, or HEAD skew) — a fully-fresh in-sync git fixture omits it
	// (QA 2026-10-19). The structured provenance field is the canonical home
	// of the index identity and must always carry the recorded commit.
	if ix, ok := prov["index"].(map[string]any); !ok || ix["git_commit"] == "" || ix["git_commit"] == nil {
		t.Fatalf("structured provenance must carry the recorded git commit: %s", text)
	}
	if strings.Contains(text, "[kern] index:") {
		t.Fatalf("fresh in-sync index must omit the one-line text stamp, got: %s", text)
	}
}

// TestE2EExplore drives kern_explore over HTTP against a real indexed symbol
// (NewServer) from the testfixture repo, exercising the graph/explore leaf.
func TestE2EExplore(t *testing.T) {
	root := fixtureRoot(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 2, "kern_explore", map[string]any{"root": root, "symbol": "NewServer"})
	text := e2eText(t, "kern_explore", resp)
	if !strings.Contains(text, "NewServer") {
		t.Fatalf("explore did not return NewServer: %s", text)
	}
	if prov := e2eProvenance(resp); prov == nil {
		t.Fatalf("kern_explore is index-backed and must carry structured provenance: %s", text)
	}
}

// TestE2EImpact drives kern_impact over HTTP against the fixture — the
// high-level analysis surface that survived the v0.9.11 slim (kern_what_if
// was removed from dispatch; impact is its served sibling and shares the
// change resolver). A valid symbol must render the IMPACT bill naming the
// change target; a garbage change must fail with the visible not-found
// error (the same "no symbol named" contract the what-if handler pins),
// never a false clean bill; the risk=true variant must render the risk
// assessment; a missing change is a validation error.
func TestE2EImpact(t *testing.T) {
	root := fixtureRoot(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	// Valid fixture symbol: the impact report runs, names the change
	// target and carries no not-found warning.
	resp := e2eCall(t, s, 300, "kern_impact", map[string]any{"root": root, "change": "NewServer"})
	assertE2EResult(t, "kern_impact/valid", resp, e2eAssertion{
		MustContain:    []string{"IMPACT for:", "NewServer"},
		MustNotContain: []string{"not found", "warning:"},
	})

	// Garbage change: the symbol does not exist in the fixture index, so
	// the call must fail visibly with the not-found error — and the error
	// text can never be mistaken for a clean "safe to proceed" bill.
	resp = e2eCall(t, s, 301, "kern_impact", map[string]any{"root": root, "change": "ZZZgarbageSymbol"})
	errText := assertE2EToolError(t, "kern_impact/garbage", resp, "no symbol named")
	if strings.Contains(errText, "Safe to proceed") {
		t.Errorf("kern_impact/garbage: unresolvable change must never read as safe to proceed: %s", errText)
	}

	// risk=true serves the former kern_risk contract: the governance risk
	// assessment, headed with the analyzed change.
	resp = e2eCall(t, s, 302, "kern_impact", map[string]any{"root": root, "change": "NewServer", "risk": "true"})
	assertE2EResult(t, "kern_impact/risk", resp, e2eAssertion{
		MustContain: []string{"RISK for:", "NewServer"},
	})

	// A missing change is rejected with the validation error.
	resp = e2eCall(t, s, 303, "kern_impact", map[string]any{"root": root})
	assertE2EToolError(t, "kern_impact/missing change", resp, "change is required")
}

// TestE2EGraph drives kern_graph over HTTP against the fixture: the default
// call-graph neighbourhood render for a real indexed symbol (with structured
// provenance, the governed graph family's stamp), an unknown symbol failing
// closed with the unknown-symbol error, and a missing symbol rejected as a
// validation error.
func TestE2EGraph(t *testing.T) {
	root := fixtureRoot(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 400, "kern_graph", map[string]any{"root": root, "symbol": "NewServer"})
	assertE2EResult(t, "kern_graph/valid", resp, e2eAssertion{
		MustContain: []string{"NewServer"},
	})
	if prov := e2eProvenance(resp); prov == nil {
		t.Errorf("kern_graph is index-backed and must carry structured provenance")
	}

	resp = e2eCall(t, s, 401, "kern_graph", map[string]any{"root": root, "symbol": "ZZZgarbageSymbol"})
	assertE2EToolError(t, "kern_graph/unknown symbol", resp, "unknown symbol")

	resp = e2eCall(t, s, 402, "kern_graph", map[string]any{"root": root})
	assertE2EToolError(t, "kern_graph/missing symbol", resp, "symbol is required")
}

// TestE2EMeta drives the kern_meta NL router over HTTP end to end: the
// request classifies to kern_explore (deterministic keyword matching) and the
// routed handler runs against the fixture index, so the response shows the
// classification and the explored symbol.
func TestE2EMeta(t *testing.T) {
	root := fixtureRoot(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 6, "kern_meta", map[string]any{"request": "how does NewServer work?", "root": root})
	text := e2eText(t, "kern_meta", resp)
	if !strings.Contains(text, "kern_explore") {
		t.Fatalf("expected kern_explore classification, got: %s", text)
	}
	if !strings.Contains(text, "NewServer") {
		t.Fatalf("expected routed explore result for NewServer, got: %s", text)
	}
}
