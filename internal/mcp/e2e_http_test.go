package mcp

// End-to-end JSON-RPC tool tests (Finding 10): real kern_* tool calls POSTed
// through the server's HTTP transport (ServeHTTP via httptest). Unlike the
// protocol-only tests in http_test.go, these exercise the full
// dispatch→allowlist→schema-negotiation→arg-validation→handler→leaf path for
// representative tools across the catalog: symbol search, graph/explore,
// progressive-disclosure retrieval, an org-family admin tool, a high-level
// orchestration tool and the kern_meta NL router. Each test asserts the
// JSON-RPC result shape (no rpc error, expected content, provenance where the
// tool is index-backed).

import (
	"encoding/json"
	"net/http"
	"path/filepath"
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
	if prov := e2eProvenance(resp); prov == nil {
		t.Fatalf("kern_search is index-backed and must carry structured provenance: %s", text)
	}
	if !strings.Contains(text, "[kern] index:") {
		t.Fatalf("expected provenance stamp in search text, got: %s", text)
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

// TestE2ERetrieve drives kern_retrieve over HTTP at disclosure level l2
// (neighborhood) for a fixture symbol — the progressive-disclosure leaf.
func TestE2ERetrieve(t *testing.T) {
	root := fixtureRoot(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 3, "kern_retrieve", map[string]any{"root": root, "symbol": "NewServer", "level": "l2"})
	text := e2eText(t, "kern_retrieve", resp)
	if !strings.Contains(text, "NewServer") {
		t.Fatalf("retrieve l2 did not return NewServer context: %s", text)
	}
}

// TestE2EOrgProjects drives the org-family admin tool kern_org_projects over
// HTTP: with no projects arg it returns the default single project named
// after the root's base name as JSON — no enterprise server needed.
func TestE2EOrgProjects(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_PRELOAD", "0")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 4, "kern_org_projects", map[string]any{"root": root})
	text := e2eText(t, "kern_org_projects", resp)
	// The handler returns indented JSON; decode and assert the shape.
	var projects struct {
		Projects []struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"projects"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(text), &projects); err != nil {
		t.Fatalf("org projects output is not JSON: %v\n%s", err, text)
	}
	if projects.Count != 1 {
		t.Fatalf("expected exactly one default project, got %d: %s", projects.Count, text)
	}
	if len(projects.Projects) == 0 {
		t.Fatalf("count says 1 but projects list is empty: %s", text)
	}
	if projects.Projects[0].Name != filepath.Base(root) {
		t.Fatalf("default project should be named after the root base, got %q: %s", projects.Projects[0].Name, text)
	}
}

// TestE2EWhatIf drives the high-level orchestration tool kern_what_if over
// HTTP against a real fixture symbol: the TaskService path runs and the
// result carries the task stamp without a not-found warning.
func TestE2EWhatIf(t *testing.T) {
	root := mcpProject(t) // tiny module with func Greet
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_PRELOAD", "0")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := newHTTPServer()

	resp := e2eCall(t, s, 5, "kern_what_if", map[string]any{"root": root, "change": "Greet"})
	text := e2eText(t, "kern_what_if", resp)
	if !strings.Contains(text, "task:") {
		t.Fatalf("expected TaskService task stamp, got: %s", text)
	}
	if strings.Contains(text, "not found") {
		t.Fatalf("valid symbol must not carry the not-found warning, got: %s", text)
	}
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
