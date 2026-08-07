package mcp

import (
	"bytes"
	"context"
	"encoding/json"
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
