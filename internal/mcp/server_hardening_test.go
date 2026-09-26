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
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
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
	// kern_sandbox is a governed exec surface; opt in so the test exercises
	// the actual execution path rather than the governance denial.
	t.Setenv("KERN_ALLOW_EXEC", "1")

	// All index-backed calls share one root, so one server builds the index
	// once instead of once per call; the calls stay sequential (slow tools
	// emit progress notifications, handled by the batch harness).
	resps := mcpBatch(t, []mcpCallSpec{
		{"kern_validate", map[string]any{"root": root, "timeout": "60"}},
		{"kern_validate", map[string]any{"root": root, "command": "true", "raw": "true"}},
		{"kern_sandbox", map[string]any{"root": root, "command": "sh -c 'printf ok'"}},
		{"kern_frameworks", rootArg},
		{"kern_swap", map[string]any{"root": root, "text": "this is a short sample"}},
		{"kern_doc_index", rootArg},
		{"kern_doc_search", map[string]any{"root": root, "query": "package", "k": "3"}},
		{"kern_precache", rootArg},
		{"kern_probe", map[string]any{"root": root, "task": "Greet"}},
		{"kern_repo_search", map[string]any{"root": root, "query": "definitely-no-such-symbol-xyz"}},
		{"kern_heal", map[string]any{"root": root, "max_rounds": "1", "timeout": "60"}},
	})
	assertOK := func(i int) string {
		t.Helper()
		if e, ok := resps[i]["error"].(map[string]any); ok {
			t.Fatalf("tool returned error: %+v", e)
		}
		text, isErr := toolResultText(t, resps[i])
		if isErr {
			t.Fatalf("tool returned isError result: %s", text)
		}
		return text
	}
	out := assertOK(9) // kern_repo_search
	if !strings.Contains(out, "no symbols matched") {
		t.Logf("kern_repo_search returned: %q", out)
	}
	// kern_heal on a healthy project returns immediately without an LLM.
	out = assertOK(10) // kern_heal
	if !strings.Contains(out, "healed OK") {
		t.Fatalf("expected healed OK on healthy project, got %q", out)
	}
	for i := 0; i < len(resps); i++ {
		switch i {
		case 9, 10:
			continue // asserted above
		default:
			assertOK(i)
		}
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
	t.Setenv("KERN_ALLOW_EXEC", "1") // kern_sandbox is a governed exec surface
	root := mcpProject(t)
	// Progress is only emitted when the client supplies a progressToken in
	// the request's _meta (MCP spec — no unsolicited tokens); include one so
	// the slow tool still fires notifications.
	args, _ := json.Marshal(map[string]any{"root": root, "command": "sh -c 'printf ok'"})
	req := writeReq("tools/call", 50, `{"name":"kern_sandbox","arguments":`+string(args)+`,"_meta":{"progressToken":"t1"}}`)
	in := strings.NewReader(req + "\n")
	buf := &bytes.Buffer{}
	s := NewServer(in, buf)
	// Test root is a temp dir outside the process cwd; confine to everything.
	s.roots = []string{"/"}
	// Disable the fail-closed-to-cwd KERN_MCP_ROOTS gate for this harness (see
	// serveMany); the gate's own tests exercise it explicitly.
	s.gate = nil
	s.preTool = nil
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
		if progressCount == 0 {
			params, _ := m["params"].(map[string]any)
			if params["progressToken"] != "t1" {
				t.Fatalf("progress notification must carry the client's progressToken, got %+v", m)
			}
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
	t.Setenv("KERN_ALLOW_EXEC", "1") // kern_sandbox is a governed exec surface
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestCancelRequestAbortsRealToolCall pins the production registration
// path: a real tools/call through dispatch registers its cancel func in
// toolCallResponse, and $/cancelRequest for that id aborts the in-flight
// handler promptly instead of waiting out the 30-minute ceiling. The other
// TestCancelRequest* tests shortcut registration via registerInflight
// directly; this one guards the wiring a transport split must preserve.
func TestCancelRequestAbortsRealToolCall(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0") // hermetic: no background index build
	dispatchTable["kern_test_cancel"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		<-ctx.Done()
		return "handler observed: " + ctx.Err().Error(), nil
	}
	defer delete(dispatchTable, "kern_test_cancel")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)

	done := make(chan any, 1)
	go func() {
		done <- s.dispatch(rpcRequest{ID: json.RawMessage(`99`), Method: "tools/call", Params: json.RawMessage(`{"name":"kern_test_cancel","arguments":{}}`)})
	}()

	// The production path must register the call as in-flight.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Inflight() != 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if s.Inflight() != 1 {
		t.Fatalf("expected tools/call to register in-flight, got %d", s.Inflight())
	}

	// Cancel via the JSON-RPC notification; the handler must unblock now.
	s.dispatch(rpcRequest{Method: "$/cancelRequest", Params: json.RawMessage(`{"id":99}`)})

	select {
	case resp := <-done:
		b, _ := json.Marshal(resp)
		if !strings.Contains(string(b), "handler observed: context canceled") {
			t.Fatalf("expected cancellation to reach the in-flight handler, got %s", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tools/call did not return after $/cancelRequest (still waiting out the 30m ceiling?)")
	}
	if s.Inflight() != 0 {
		t.Fatalf("expected in-flight to drain after cancellation, got %d", s.Inflight())
	}
}

// TestIdKeyCanonicalization pins the id-key forms so tools/call and
// $/cancelRequest agree on numbers, strings and raw ids.
func TestIdKeyCanonicalization(t *testing.T) {
	t.Parallel()
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
// cancels every in-flight tool and frees every lock held by the server, while
// leaving the inflight map intact so shutdown can wait for running tools to
// drain (each tool goroutine removes itself via unregisterInflight).
func TestCancelAllClearsInflightAndReleasesLocks(t *testing.T) {
	t.Parallel()
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
	// The inflight entry must survive CancelAll (still counted by Inflight())
	// so the shutdown drain can wait for the tool goroutine to unregister it.
	if _, ok := s.inflight["9"]; !ok {
		t.Fatalf("expected inflight entry to remain after CancelAll, got %v", s.inflight)
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
	t.Parallel()
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	if _, err := s.loadIndex(context.Background(), root); err != nil {
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
	t.Parallel()
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
	// KERN_MCP_ROOTS is an accepted alias for the workspace roots; clear it
	// so an ambient value cannot leak into this single-root assertion.
	t.Setenv("KERN_MCP_ROOTS", "")
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

// TestRootConfinementBlocksValidateRoot verifies that kern_validate's root
// argument (the run_build merge absorbed its dir handling into root) is
// confined to the server workspace like every other path argument.
func TestRootConfinementBlocksValidateRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_EXEC", "1") // so sandbox succeeds once inside the workspace
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard) // confined to cwd
	ctx := context.Background()
	_, err := s.runTool(ctx, "1", "", "kern_validate", map[string]any{"root": root, "command": "cat /etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected workspace confinement error, got %v", err)
	}
	_, err = s.runTool(ctx, "1", "", "kern_sandbox", map[string]any{"root": root, "command": "echo hi"})
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected sandbox confinement error, got %v", err)
	}
	// Same server, workspace extended to the project root: both allowed.
	s.roots = []string{root}
	if _, err := s.runTool(ctx, "1", "", "kern_sandbox", map[string]any{"root": root, "command": "echo hi"}); err != nil {
		t.Fatalf("sandbox inside workspace should run: %v", err)
	}
	if _, err := s.runTool(ctx, "1", "", "kern_validate", map[string]any{"root": root, "command": "echo ok", "raw": "true"}); err != nil {
		t.Fatalf("validate inside workspace should run: %v", err)
	}
}

// TestSandboxManifestViaMCP verifies the kern_sandbox tool surfaces the
// post-run impact manifest (created/modified/deleted lines + summary) when the
// command changes the tree.
func TestSandboxManifestViaMCP(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")       // kern_sandbox is a governed exec surface
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	root := mcpProject(t)
	out := mcpLastOK(t, "kern_sandbox", map[string]any{"root": root, "command": "sh -c 'echo x > created.go && echo y >> app.go'"})
	if !strings.Contains(out, "=== sandbox impact manifest ===") {
		t.Fatalf("expected manifest section in output, got %q", out)
	}
	if !strings.Contains(out, "+ created.go (") {
		t.Fatalf("expected created entry for created.go, got %q", out)
	}
	if !strings.Contains(out, "~ app.go (") {
		t.Fatalf("expected modified entry for app.go, got %q", out)
	}
	if !strings.Contains(out, "2 change(s)") {
		t.Fatalf("expected summary line, got %q", out)
	}
}

func TestSlowToolEmitsProgress(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := slowTools
	slowTools = map[string]bool{"kern_test_slow": true}
	defer func() { slowTools = old }()
	dispatchTable["kern_test_slow"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return "slow done", nil
	}
	defer delete(dispatchTable, "kern_test_slow")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	out, err := s.runTool(context.Background(), "77", "p1", "kern_test_slow", map[string]any{})
	if err != nil {
		t.Fatalf("runTool: %v", err)
	}
	if out != "slow done" {
		t.Fatalf("out = %q, want slow done", out)
	}
	lines := splitNonEmpty(buf.String())
	if len(lines) < 2 {
		t.Fatalf("expected 0%% and 100%% progress notifications, got %d lines: %q", len(lines), buf.String())
	}
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("bad line %q: %v", ln, err)
		}
		if m["method"] != "notifications/progress" {
			t.Fatalf("expected progress notification, got %+v", m)
		}
	}
}

func TestFastToolEmitsNoProgress(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Ensure the fake fast tool is not slow (the set derives from the
	// catalog's per-tool Slow flag).
	if slowTools["kern_test_fast"] {
		t.Fatal("kern_test_fast must not be a slow tool")
	}
	dispatchTable["kern_test_fast"] = func(s *Server, ctx context.Context, id string, args map[string]any) (string, error) {
		return "fast done", nil
	}
	defer delete(dispatchTable, "kern_test_fast")

	buf := &bytes.Buffer{}
	s := NewServer(strings.NewReader(""), buf)
	out, err := s.runTool(context.Background(), "88", "", "kern_test_fast", map[string]any{})
	if err != nil {
		t.Fatalf("runTool: %v", err)
	}
	if out != "fast done" {
		t.Fatalf("out = %q, want fast done", out)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("fast tool emitted progress notifications: %q", got)
	}
}

func TestSlowToolSetDerivedFromCatalog(t *testing.T) {
	t.Parallel()
	slow := map[string]bool{}
	for _, t := range catalog.All {
		if t.Slow {
			slow[t.Name] = true
		}
	}
	// The set is derived: server slowTools must equal the catalog flags.
	if len(slowTools) != len(slow) {
		t.Fatalf("slowTools (%d) does not match catalog slow flags (%d)", len(slowTools), len(slow))
	}
	for name := range slow {
		if !slowTools[name] {
			t.Errorf("catalog slow tool %s missing from server slowTools", name)
		}
	}
	for name := range slowTools {
		if !slow[name] {
			t.Errorf("server slowTools %s not flagged Slow in the catalog", name)
		}
	}
	// The task contract: index/build/scan tools emit progress…
	for _, want := range []string{"kern_heal", "kern_validate", "kern_sandbox", "kern_refactor_transaction", "kern_repair_diagnostics", "kern_doc_index"} {
		if !slowTools[want] {
			t.Errorf("expected %s to be a slow (progress-emitting) tool", want)
		}
	}
	// …and fast lookups stay silent.
	for _, want := range []string{"kern_search", "kern_explore", "kern_graph", "kern_context", "kern_compact_file"} {
		if slowTools[want] {
			t.Errorf("expected %s to be a fast (no-progress) tool", want)
		}
	}
}
