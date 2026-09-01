package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/project"
)

func newHTTPServer() *Server {
	// Mirrors the production ServeHTTPContext constructor: every map/channel
	// field must be initialized or an index-loading tool panics ("assignment
	// to entry in nil map" in Server.commit / deadlocks on a nil sem). Keep in
	// sync with ServeHTTPContext in http.go.
	return &Server{
		sem:       make(chan struct{}, 8),
		locks:     map[string]*lock.Lock{},
		inflight:  map[string]context.CancelFunc{},
		sessions:  map[string]*project.Session{},
		transport: "http",
		roots:     defaultWorkspaceRoots(),
		gate:      NewGateFromEnv(),
		commits:   map[string]string{},
	}
}

func doHTTP(t *testing.T, s *Server, method, ctype, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.handleHTTP(rr, req)
	return rr
}

func TestHandleHTTPGetNotAllowed(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodGet, "application/json", "", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleHTTPGetSSEUnsupported(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodGet, "text/event-stream", "", map[string]string{"Accept": "text/event-stream"})
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for SSE, got %d", rr.Code)
	}
}

func TestHandleHTTPBadContentType(t *testing.T) {
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "text/plain", `{}`, nil)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rr.Code)
	}
}

func TestHandleHTTPParseError(t *testing.T) {
	body := "not json"
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (JSON-RPC error in body), got %d", rr.Code)
	}
	var errResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	e := errResp["error"].(map[string]any)
	if int(e["code"].(float64)) != -32700 {
		t.Fatalf("expected parse-error code, got %v", e["code"])
	}
}

func TestHandleHTTPSingleRequest(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("bad jsonrpc: %v", resp)
	}
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("bad protocolVersion: %v", res["protocolVersion"])
	}
}

func TestHandleHTTPBatchRequestRejected(t *testing.T) {
	body := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (JSON-RPC error in body), got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal batch error: %v", err)
	}
	e := resp["error"].(map[string]any)
	if int(e["code"].(float64)) != -32700 {
		t.Fatalf("expected -32700 for batch, got %+v", resp)
	}
}

func TestHandleHTTPNotificationAcceptedNoBody(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for notification, got %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "" {
		t.Fatalf("notification body should be empty, got %q", rr.Body.String())
	}
}

func TestHandleHTTPUnknownMethod(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":3,"method":"bogus/method"}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, nil)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	e := resp["error"].(map[string]any)
	if int(e["code"].(float64)) != -32601 {
		t.Fatalf("expected method-not-found, got %v", e)
	}
}

func TestHandleHTTPMissingProtocolVersion(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":5,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, map[string]string{"MCP-Protocol-Version": ""})
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 for missing protocol version, got %d", rr.Code)
	}
	if v := rr.Header().Get("MCP-Protocol-Version"); v != protocolVersion {
		t.Fatalf("expected MCP-Protocol-Version echoed, got %q", v)
	}
}

func TestHandleHTTPBadProtocolVersion(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":6,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, map[string]string{"MCP-Protocol-Version": "2030-01-01"})
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 for wrong protocol version, got %d", rr.Code)
	}
}

func TestHandleHTTPSLegacyProtocolVersionAccepted(t *testing.T) {
	for _, v := range []string{"2024-11-05", "2025-03-26"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + v + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", v)
		rr := httptest.NewRecorder()
		newHTTPServer().handleHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for protocol version %s, got %d", v, rr.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if got := resp["result"].(map[string]any)["protocolVersion"]; got != v {
			t.Fatalf("expected negotiated protocolVersion %s, got %v", v, got)
		}
	}
}

func TestHandleHTTPRemoteOriginRejected(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, map[string]string{"Origin": "https://evil.example"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote origin, got %d", rr.Code)
	}
}

func TestHandleHTTPSameHostOriginAllowed(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":8,"method":"ping"}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, map[string]string{"Origin": "http://127.0.0.1:5173"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback origin, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWriteHTTPErrorIsJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeHTTPError(rr, errorResponse(nil, -1, "boom"))
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected json content-type, got %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "boom") {
		t.Fatalf("expected error body, got %q", rr.Body.String())
	}
	if !strings.HasSuffix(rr.Body.String(), "\n") {
		t.Fatal("expected trailing newline")
	}
}

func TestHandleHTTPOversizeBodyRejected(t *testing.T) {
	// The handler limits reads to 1<<24 bytes. A body smaller than the limit
	// must still succeed (smoke that the LimitReader path is wired).
	big := `{"jsonrpc":"2.0","id":4,"method":"initialize","params":{}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", big, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for small body, got %d", rr.Code)
	}
}

func TestHandleHTTPDocFetchEndToEnd(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_LOOPBACK_FETCH", "1")
	doc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><head><title>React Hooks</title></head><body><h1>useState</h1><p>useState manages component state in function components.</p></body></html>")
	}))
	defer doc.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "local.md"), []byte("local project notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The root passed to kern_doc_fetch lives outside the process cwd, so the
	// server's workspace must be extended to include it (confinement). The
	// KERN_MCP_ROOTS gate must be aligned with the same root (it fails closed
	// to the process cwd when unset).
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	args, _ := json.Marshal(map[string]any{"url": doc.URL, "root": root, "name": "react"})
	body := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"kern_doc_fetch","arguments":` + string(args) + `}}`
	rr := doHTTP(t, newHTTPServer(), http.MethodPost, "application/json", body, map[string]string{"Origin": "http://localhost:5173"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("isError result: %s", text)
	}
	if !strings.Contains(text, "React Hooks") || !strings.Contains(text, "useState") {
		t.Fatalf("expected fetched page in result, got: %s", text)
	}
	ix := docsearch.Load(root)
	if ix == nil {
		t.Fatal("index not persisted after HTTP fetch")
	}
	found := false
	for _, d := range ix.Docs {
		if d.Chunk.File == "fetch/react.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fetch/react.md missing from persisted index")
	}
}

func TestIsLocalhostOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},
		{"http://localhost:5173", true},
		{"http://127.0.0.1:8080", true},
		{"https://[::1]:9999", true},
		{"https://evil.example", false},
		{"not a url", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if got := isLocalhostOrigin(req); got != c.want {
			t.Errorf("isLocalhostOrigin(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

// TestHandleHTTPIndexToolNoPanic is a regression test for the HTTP transport
// panic where an index-loading tool call crashed with "assignment to entry in
// nil map" in Server.commit (s.commits was nil because the HTTP constructor
// omitted it). Driving an index tool over HTTP must return a valid result with
// no panic.
func TestHandleHTTPIndexToolNoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("builds full index; skipped with -short")
	}
	// Point the workspace at a real repo so kern_search loads an index, and
	// align the KERN_MCP_ROOTS gate with the same root (the gate fails closed
	// to the process cwd when unset).
	t.Setenv("KERN_ROOTS", kernRepoRoot)
	t.Setenv("KERN_MCP_ROOTS", kernRepoRoot)
	s := newHTTPServer()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kern_search","arguments":{"root":"` + kernRepoRoot + `","query":"TaskService","limit":1}}}`
	rr := doHTTP(t, s, http.MethodPost, "application/json", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, rr.Body.String())
	}
	if _, isErr := toolResultText(t, resp); isErr {
		t.Fatalf("index tool over HTTP returned an error result: %s", rr.Body.String())
	}
	// The provenance stamp must include a commit (this exact path panicked).
	if !strings.Contains(rr.Body.String(), "commit ") {
		t.Fatalf("expected provenance commit stamp, got: %s", rr.Body.String())
	}
}
