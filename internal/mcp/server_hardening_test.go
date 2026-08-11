package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// mcpCallLast runs a tools/call and returns the final response, skipping any
// progress notifications that slow tools emit before answering.
func mcpCallLast(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	pa, _ := json.Marshal(args)
	params := `{"name":"` + name + `","arguments":` + string(pa) + `}`
	resps := serveMany(t, writeReq("tools/call", name, params))
	if len(resps) == 0 {
		t.Fatalf("no responses for %s", name)
	}
	return resps[len(resps)-1]
}

func mcpLastOK(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	resp := mcpCallLast(t, name, args)
	if e, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("tool %s returned error: %+v", name, e)
	}
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("tool %s returned isError result: %s", name, text)
	}
	return text
}

// TestToolCallCoverage exercises every tool registered in runTool through a
// real tools/call so a dispatch typo, missing arg check, or index build crash
// is caught per tool instead of only when the tool happens to be used.
func TestToolCallCoverage(t *testing.T) {
	root := mcpProject(t)
	rootArg := map[string]any{"root": root}

	mcpLastOK(t, "kern_validate", map[string]any{"root": root, "timeout": "60"})
	mcpLastOK(t, "kern_run_build", map[string]any{"command": "true", "dir": root})
	mcpLastOK(t, "kern_sandbox", map[string]any{"root": root, "command": "sh -c 'printf ok'"})
	mcpLastOK(t, "kern_frameworks", rootArg)
	mcpLastOK(t, "kern_swap", map[string]any{"root": root, "text": "this is a short sample"})
	mcpLastOK(t, "kern_doc_index", rootArg)
	mcpLastOK(t, "kern_doc_search", map[string]any{"root": root, "query": "package", "k": "3"})
	mcpLastOK(t, "kern_precache", rootArg)
	mcpLastOK(t, "kern_probe", map[string]any{"root": root, "task": "Greet"})
	out := mcpLastOK(t, "kern_repo_search", map[string]any{"query": "definitely-no-such-symbol-xyz"})
	if !strings.Contains(out, "no symbols matched") {
		t.Logf("kern_repo_search returned: %q", out)
	}

	// kern_heal on a healthy project returns immediately without an LLM.
	out = mcpLastOK(t, "kern_heal", map[string]any{"root": root, "max_rounds": "1", "timeout": "60"})
	if !strings.Contains(out, "healed OK") {
		t.Fatalf("expected healed OK on healthy project, got %q", out)
	}
}

func TestDocFetchMergesIntoLocalIndex(t *testing.T) {
	t.Setenv("KERN_ALLOW_LOOPBACK_FETCH", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>Widgets API</title>
<h1>Widget API</h1>
<p>The <code>MakeWidget</code> function creates a widget instance. It takes a name
and returns a handle. Call <code>widget.Release</code> to free resources.</p>`))
	}))
	defer srv.Close()

	root := t.TempDir()
	out := mcpLastOK(t, "kern_doc_fetch", map[string]any{
		"url":  srv.URL,
		"root": root,
		"name": "widget-api",
	})
	if !strings.Contains(out, "widget-api") || !strings.Contains(out, "MakeWidget") {
		t.Fatalf("fetch summary missing content: %q", out)
	}

	// The fetched page must now be findable via the local doc index.
	res := mcpLastOK(t, "kern_doc_search", map[string]any{"root": root, "query": "MakeWidget release", "k": "2"})
	if !strings.Contains(res, "fetch/widget-api.md") {
		t.Fatalf("fetched page not searchable, got: %q", res)
	}
}

// TestProgressNotificationsBeforeResult verifies the stdio transport emits
// progress notifications (0% and 100%) before the tools/call result and never
// after it.
func TestProgressNotificationsBeforeResult(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)
	req := toolsCallJSON(t, 50, "kern_sandbox", map[string]any{"root": root, "command": "sh -c 'printf ok'"})
	in := strings.NewReader(req + "\n")
	buf := &bytes.Buffer{}
	s := NewServer(in, buf)
	// Test root is a temp dir outside the process cwd; confine to everything.
	s.roots = []string{"/"}
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := splitNonEmpty(buf.String())
	if len(lines) < 3 {
		t.Fatalf("expected progress + result, got %d lines:\n%s", len(lines), buf.String())
	}
	progressCount := 0
	for _, ln := range lines[:len(lines)-1] {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("bad line %q: %v", ln, err)
		}
		if m["method"] != "notifications/progress" {
			t.Fatalf("expected progress notification before result, got %+v", m)
		}
		progressCount++
	}
	if progressCount < 2 {
		t.Fatalf("expected 0%% and 100%% progress notifications, got %d", progressCount)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["id"] != float64(50) {
		t.Fatalf("expected final result for id 50, got %+v", last)
	}
	if text, isErr := toolResultText(t, last); isErr {
		t.Fatalf("sandbox tool errored: %s", text)
	}
}

// TestHTTPNoProgressNotifications verifies the HTTP transport returns exactly
// the tool result without any push-style notifications.
func TestHTTPNoProgressNotifications(t *testing.T) {
	root := mcpProject(t)
	params := map[string]any{
		"jsonrpc": "2.0", "id": 60, "method": "tools/call",
		"params": map[string]any{"name": "kern_sandbox", "arguments": map[string]any{"root": root, "command": "sh -c 'printf ok'"}},
	}
	body, _ := json.Marshal(params)
	rr := doHTTP(t, newHTTPServer(), "POST", "application/json", string(body), nil)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "notifications/progress") {
		t.Fatalf("HTTP transport must not emit progress notifications: %s", rr.Body.String())
	}
}

// TestCancelRequestAbortsInflight verifies $/cancelRequest cancels the context
// of a running tool call.
func TestCancelRequestAbortsInflight(t *testing.T) {
	s := &Server{transport: "stdio"}
	ctx, cancel := context.WithCancel(context.Background())
	s.registerInflight("77", cancel)
	resp := s.dispatch(rpcRequest{ID: json.RawMessage(`77`), Method: "$/cancelRequest", Params: json.RawMessage(`{"id":77}`)})
	if resp == nil {
		t.Fatal("expected a response to $/cancelRequest")
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("expected inflight context cancelled, got %v", err)
	}
}

// TestCancelRequestAsNotification verifies $/cancelRequest still cancels when
// sent as a JSON-RPC notification (no id), which is how spec-compliant clients
// deliver it, and that no response is produced.
func TestCancelRequestAsNotification(t *testing.T) {
	s := &Server{transport: "stdio"}
	ctx, cancel := context.WithCancel(context.Background())
	s.registerInflight("42", cancel)
	resp := s.dispatch(rpcRequest{Method: "$/cancelRequest", Params: json.RawMessage(`{"id":42}`)})
	if resp != nil {
		t.Fatalf("notification-form cancel must not produce a response, got %+v", resp)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("expected inflight context cancelled, got %v", err)
	}
}

// TestCancelRequestStringID verifies the in-flight key matches between
// tools/call registration and $/cancelRequest when the client uses a string
// id (idKey canonicalizes both sides).
func TestCancelRequestStringID(t *testing.T) {
	s := &Server{transport: "stdio"}
	ctx, cancel := context.WithCancel(context.Background())
	s.registerInflight(idKey(json.RawMessage(`"abc"`)), cancel)
	resp := s.dispatch(rpcRequest{ID: json.RawMessage(`"abc"`), Method: "$/cancelRequest", Params: json.RawMessage(`{"id":"abc"}`)})
	if resp == nil {
		t.Fatal("expected a response to $/cancelRequest")
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("expected inflight context cancelled for string id, got %v", err)
	}
}

// TestIdKeyCanonicalization pins the id-key forms so tools/call and
// $/cancelRequest agree on numbers, strings and raw ids.
func TestIdKeyCanonicalization(t *testing.T) {
	cases := []struct {
		id   json.RawMessage
		want string
	}{
		{json.RawMessage(`77`), "77"},
		{json.RawMessage(`77.0`), "77"},
		{json.RawMessage(`"abc"`), "abc"},
		{json.RawMessage(``), ""},
		{json.RawMessage(`notjson`), "notjson"},
	}
	for _, c := range cases {
		if got := idKey(c.id); got != c.want {
			t.Errorf("idKey(%s) = %q; want %q", c.id, got, c.want)
		}
	}
}

// TestCancelAllClearsInflightAndReleasesLocks verifies graceful shutdown
// cancels every in-flight tool and frees every lock held by the server.
func TestCancelAllClearsInflightAndReleasesLocks(t *testing.T) {
	root := t.TempDir()
	s := &Server{transport: "stdio", locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}}
	ctx, cancel := context.WithCancel(context.Background())
	s.inflight["9"] = cancel
	lk, err := lock.Acquire(root, "scope-x")
	if err != nil {
		t.Fatal(err)
	}
	s.locks["scope-x"] = lk
	s.cancelAll()
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("expected inflight cancelled, got %v", err)
	}
	if len(s.inflight) != 0 {
		t.Fatalf("expected empty inflight after CancelAll, got %v", s.inflight)
	}
	if len(s.locks) != 0 {
		t.Fatalf("expected empty locks after CancelAll, got %v", s.locks)
	}
	if held, _, _ := lock.Held(root, "scope-x"); held {
		t.Fatal("lock must be released after CancelAll")
	}
}

// TestStaleIndexRebuiltOnSecondCall ensures a cached index that went stale
// (new source file added) is rebuilt instead of served from cache.
func TestStaleIndexRebuiltOnSecondCall(t *testing.T) {
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	if _, err := s.loadIndex(root); err != nil {
		t.Fatalf("first index build: %v", err)
	}
	// Add a second file, making the cached index stale.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mcpAssertOK(t, "kern_entry_points", map[string]any{"root": root})
	_ = out
	out = mcpLastOK(t, "kern_search", map[string]any{"root": root, "query": "Extra"})
	if !strings.Contains(out, "Extra") {
		t.Fatalf("expected stale index rebuilt to include Extra, got %q", out)
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func TestRootConfinementDefaultCwd(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	if len(s.roots) != 1 {
		t.Fatalf("default server should have exactly one root (cwd), got %v", s.roots)
	}
	outside := t.TempDir()
	if err := s.checkWithinWorkspace(outside); err == nil {
		t.Fatalf("temp dir %q must be rejected by a default (cwd-confined) server", outside)
	}
	if err := s.checkWithinWorkspace("."); err != nil {
		t.Fatalf("cwd must be allowed: %v", err)
	}
}

func TestRootConfinementEnvAndSymlink(t *testing.T) {
	ws := t.TempDir()
	other := t.TempDir()
	t.Setenv("KERN_ROOTS", ws)
	s := NewServer(strings.NewReader(""), io.Discard)
	if len(s.roots) != 1 {
		t.Fatalf("KERN_ROOTS should yield exactly one root, got %v", s.roots)
	}
	if err := s.checkWithinWorkspace(ws); err != nil {
		t.Fatalf("workspace root must be allowed: %v", err)
	}
	if err := s.checkWithinWorkspace(filepath.Join(ws, "sub", "new")); err != nil {
		t.Fatalf("nonexistent descendant of workspace root must be allowed: %v", err)
	}
	if err := s.checkWithinWorkspace(other); err == nil {
		t.Fatal("dir outside KERN_ROOTS must be rejected")
	}
	// A symlink inside the workspace that points outside must be rejected: its
	// text lives inside, its target does not.
	link := filepath.Join(ws, "escape")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWithinWorkspace(link); err == nil {
		t.Fatal("symlink escaping the workspace must be rejected")
	}
	// And a symlink pointing back into the workspace stays allowed.
	link2 := filepath.Join(other, "back")
	if err := os.Symlink(ws, link2); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWithinWorkspace(link2); err != nil {
		t.Fatalf("symlink into the workspace must be allowed: %v", err)
	}
}

func TestRootConfinementBlocksRunBuildDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard) // confined to cwd
	ctx := context.Background()
	_, err := s.runTool(ctx, "1", "kern_run_build", map[string]any{"command": "cat /etc/passwd", "dir": root})
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected workspace confinement error, got %v", err)
	}
	_, err = s.runTool(ctx, "1", "kern_sandbox", map[string]any{"root": root, "command": "echo hi"})
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected sandbox confinement error, got %v", err)
	}
	// Same server, workspace extended to the project root: both allowed.
	s.roots = []string{root}
	if _, err := s.runTool(ctx, "1", "kern_sandbox", map[string]any{"root": root, "command": "echo hi"}); err != nil {
		t.Fatalf("sandbox inside workspace should run: %v", err)
	}
	if _, err := s.runTool(ctx, "1", "kern_run_build", map[string]any{"command": "echo ok", "dir": root}); err != nil {
		t.Fatalf("run_build inside workspace should run: %v", err)
	}
}
