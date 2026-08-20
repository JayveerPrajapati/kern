package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeReq(method string, id any, params string) string {
	if params == "" {
		return `{"jsonrpc":"2.0","id":` + jsonID(id) + `,"method":"` + method + `"}`
	}
	return `{"jsonrpc":"2.0","id":` + jsonID(id) + `,"method":"` + method + `","params":` + params + `}`
}

func jsonID(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// serveMany feeds several requests into a single Server (so per-server state
// such as the lock table is preserved) and returns each decoded response.
func serveMany(t *testing.T, reqs ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(reqs, "\n") + "\n")
	buf := &bytes.Buffer{}
	s := NewServer(in, buf)
	// Tests use temp-dir roots outside the process cwd; confine to everything.
	s.roots = []string{"/"}
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal response %q: %v", line, err)
		}
		resp = append(resp, r)
	}
	return resp
}

func serveOne(t *testing.T, req string) map[string]any {
	t.Helper()
	resps := serveMany(t, req)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	return resps[0]
}

func toolResultText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	isErr, _ := res["isError"].(bool)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return "", isErr
	}
	first, _ := content[0].(map[string]any)
	return first["text"].(string), isErr
}

func TestInitialize(t *testing.T) {
	resp := serveOne(t, writeReq("initialize", 1, `{"capabilities":{}}`))
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("bad jsonrpc: %v", resp)
	}
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("bad protocolVersion: %v", res["protocolVersion"])
	}
	si := res["serverInfo"].(map[string]any)
	if si["name"] != serverName || si["version"] != serverVersion {
		t.Fatalf("bad serverInfo: %v", si)
	}
}

func TestSetServerVersionPropagates(t *testing.T) {
	SetServerVersion("9.9.9-test")
	defer SetServerVersion("dev")
	resp := serveOne(t, writeReq("initialize", 1, `{"capabilities":{}}`))
	si := resp["result"].(map[string]any)["serverInfo"].(map[string]any)
	if si["version"] != "9.9.9-test" {
		t.Fatalf("expected stamped version in initialize, got %v", si["version"])
	}
	SetServerVersion("")
	if serverVersion != "9.9.9-test" {
		t.Fatalf("SetServerVersion(\"\") must not blank the version, got %q", serverVersion)
	}
}

func TestPing(t *testing.T) {
	resp := serveOne(t, writeReq("ping", 2, ``))
	if resp["result"] == nil {
		t.Fatalf("ping must return a result: %+v", resp)
	}
}

func TestToolsListAndPromptsList(t *testing.T) {
	resp := serveOne(t, writeReq("tools/list", 3, ``))
	res := resp["result"].(map[string]any)
	tl := res["tools"].([]any)
	if len(tl) == 0 {
		t.Fatal("expected non-empty tool list")
	}
	resp = serveOne(t, writeReq("prompts/list", 4, ``))
	res = resp["result"].(map[string]any)
	if len(res["prompts"].([]any)) == 0 {
		t.Fatal("expected non-empty prompt list")
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	resp := serveOne(t, writeReq("meth/no-such", 5, ``))
	err, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %+v", resp)
	}
	if int(err["code"].(float64)) != -32601 {
		t.Fatalf("expected method-not-found code, got %v", err["code"])
	}
}

func TestInvalidJSONReturnsParseError(t *testing.T) {
	in := strings.NewReader("not json\r\n" + writeReq("initialize", 6, `{}`) + "\n")
	out := &bytes.Buffer{}
	s := NewServer(in, out)
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Count(strings.TrimRight(out.String(), "\n"), "\n") + 1
	if lines != 2 {
		t.Fatalf("expected 2 responses (parse error + initialize), got %d", lines)
	}
	// First line must be the -32700 parse error, not a silent drop.
	first := strings.TrimSpace(strings.SplitN(out.String(), "\n", 2)[0])
	var e map[string]any
	if err := json.Unmarshal([]byte(first), &e); err != nil {
		t.Fatalf("bad first response %q: %v", first, err)
	}
	if errObj, ok := e["error"].(map[string]any); !ok || int(errObj["code"].(float64)) != -32700 {
		t.Fatalf("expected -32700 parse error, got %+v", e)
	}
}

func TestNotificationsInitializedNoResponse(t *testing.T) {
	out := &bytes.Buffer{}
	in := strings.NewReader(writeReq("notifications/initialized", nil, ``) + "\n")
	s := NewServer(in, out)
	if err := s.Serve(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("notification must produce no response, got %q", out.String())
	}
}

func testRoot(t *testing.T) string {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestOptimizePromptAndCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	args, _ := json.Marshal(map[string]any{"prompt": "this is a long prompt with redundant redundancy and verbose words", "cache": "true"})
	resp := serveOne(t, writeReq("tools/call", 7, `{"name":"kern_optimize_prompt","arguments":`+string(args)+`}`))
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "optimized prompt") || !strings.Contains(text, "tokens:") {
		t.Fatalf("bad optimize output: %q", text)
	}
	// Second identical call -> served from cache.
	resp2 := serveOne(t, writeReq("tools/call", 8, `{"name":"kern_optimize_prompt","arguments":`+string(args)+`}`))
	text2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error on cached call: %s", text2)
	}
	if !strings.Contains(text2, "served from exact cache") {
		t.Fatalf("expected exact-cache marker, got %q", text2)
	}
}

func TestSemanticCacheViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// First call primes the semantic cache (cache is on by default now).
	first := mcpAssertOK(t, "kern_optimize_prompt", map[string]any{"prompt": "how do I compress a very large server log file"})
	if strings.Contains(first, "served from") {
		t.Fatalf("first call must not be served from cache, got %q", first)
	}
	// Near-duplicate (one word removed) -> semantic cache hit with a marker.
	second := mcpAssertOK(t, "kern_optimize_prompt", map[string]any{"prompt": "how do I compress a very large server log"})
	if !strings.Contains(second, "served from semantic cache") {
		t.Fatalf("expected semantic cache marker, got %q", second)
	}
	if !strings.Contains(second, "similarity") {
		t.Fatalf("expected similarity reported, got %q", second)
	}
	// Explicit cache=false must bypass both caches.
	fresh := mcpAssertOK(t, "kern_optimize_prompt", map[string]any{"prompt": "how do I compress a very large server log file", "cache": "false"})
	if strings.Contains(fresh, "served from") {
		t.Fatalf("cache=false must bypass the cache, got %q", fresh)
	}
}

func TestSemcacheManagementViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mcpAssertOK(t, "kern_optimize_prompt", map[string]any{"prompt": "the database connection failed during migration"})
	mcpAssertOK(t, "kern_optimize_log", map[string]any{"log": "ERROR disk full\nERROR connection refused\nINFO start"})

	stats := mcpAssertOK(t, "kern_semcache", map[string]any{"action": "stats"})
	if !strings.Contains(stats, "prompt") || !strings.Contains(stats, "log") {
		t.Fatalf("stats should list prompt and log namespaces, got %q", stats)
	}

	list := mcpAssertOK(t, "kern_semcache", map[string]any{"action": "list", "namespace": "prompt"})
	if !strings.Contains(list, "database connection failed") {
		t.Fatalf("list should show stored prompt inputs, got %q", list)
	}

	sim := mcpAssertOK(t, "kern_semcache", map[string]any{"action": "similarity", "a": "fix the login bug", "b": "fix the login bug"})
	if !strings.Contains(sim, "similarity: 1.000") {
		t.Fatalf("identical inputs should report similarity 1.000, got %q", sim)
	}

	cleared := mcpAssertOK(t, "kern_semcache", map[string]any{"action": "clear", "namespace": "prompt"})
	if !strings.Contains(cleared, "cleared prompt") {
		t.Fatalf("expected clear confirmation, got %q", cleared)
	}
	list2 := mcpAssertOK(t, "kern_semcache", map[string]any{"action": "list", "namespace": "prompt"})
	if !strings.Contains(list2, "empty") {
		t.Fatalf("prompt namespace should be empty after clear, got %q", list2)
	}
}

func TestMaskPiiViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	text := "email me at a@b.com, key is 8.8.8.8, token ghp_1234567890abcdefghijklmnopqrstuvw"
	args, _ := json.Marshal(map[string]any{"text": text, "mask_names": "acme"})
	resp := serveOne(t, writeReq("tools/call", 9, `{"name":"kern_mask_pii","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.Contains(out, "a@b.com") || strings.Contains(out, "192.168.0.1") || strings.Contains(out, "ghp_1234567890abcdefghijklmnopqrstuvw") {
		t.Fatalf("secrets not masked: %q", out)
	}
	if !strings.Contains(out, "masked") {
		t.Fatalf("expected mask summary, got %q", out)
	}
}

func TestOptimizeLogViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	log := "[INFO] start\n[DEBUG] detail\nERROR disk full\n[INFO] done"
	args, _ := json.Marshal(map[string]any{"log": log})
	resp := serveOne(t, writeReq("tools/call", 10, `{"name":"kern_optimize_log","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "ERROR disk full") {
		t.Fatalf("critical log line lost: %q", out)
	}
}

func TestContextBudgetViaMCP(t *testing.T) {
	long := strings.Repeat("word ", 2500)
	args, _ := json.Marshal(map[string]any{"text": long, "max_tokens": "50"})
	resp := serveOne(t, writeReq("tools/call", 11, `{"name":"kern_context_budget","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "tokens") || !strings.Contains(out, "saved") {
		t.Fatalf("bad budget output: %q", out)
	}
}

func TestCompactFileViaMCP(t *testing.T) {
	root := testRoot(t)
	f := filepath.Join(root, "main.go")
	if err := os.WriteFile(f, []byte("package main\n\n// foo is a helper.\nfunc foo() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"root": root, "path": f})
	resp := serveOne(t, writeReq("tools/call", 12, `{"name":"kern_compact_file","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "foo") || !strings.Contains(out, "go") {
		t.Fatalf("bad compact output: %q", out)
	}
}

func TestProjectMapViaMCP(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"root": root})
	resp := serveOne(t, writeReq("tools/call", 13, `{"name":"kern_project_map","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, root) || !strings.Contains(out, "main.go") {
		t.Fatalf("expected project map to list root and main.go, got %q", out)
	}
}

func TestProjectMapHonorsMaxFiles(t *testing.T) {
	root := testRoot(t)
	for i, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(root, name),
			[]byte("package main\n\nfunc f"+string(rune('a'+i))+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"root": root, "max_files": "1"})
	resp := serveOne(t, writeReq("tools/call", 13, `{"name":"kern_project_map","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.Count(out, "func f") != 1 {
		t.Fatalf("expected exactly 1 file with max_files=1, got %q", out)
	}
	args2, _ := json.Marshal(map[string]any{"root": root, "max_files": "2"})
	resp2 := serveOne(t, writeReq("tools/call", 13, `{"name":"kern_project_map","arguments":`+string(args2)+`}`))
	out2, isErr := toolResultText(t, resp2)
	if isErr {
		t.Fatalf("unexpected error: %s", out2)
	}
	if strings.Count(out2, "func f") != 2 {
		t.Fatalf("expected exactly 2 files with max_files=2, got %q", out2)
	}
}

func TestAstSearchViaMCP(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc foo() {}\nfunc bar() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"root": root, "pattern": "func foo"})
	resp := serveOne(t, writeReq("tools/call", 14, `{"name":"kern_ast_search","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "foo") {
		t.Fatalf("expected foo in results, got %q", out)
	}
}

func TestVerifyOutputViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	f := filepath.Join(root, "app.go")
	_ = os.WriteFile(f, []byte("package main\n\nfunc main() {}\n"), 0o644)
	text := "see app.go:1 for main entry"
	args, _ := json.Marshal(map[string]any{"root": root, "text": text})
	resp := serveOne(t, writeReq("tools/call", 15, `{"name":"kern_verify_output","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if out == "" {
		t.Fatal("expected some verify output")
	}
}

func TestDiffFilesViaMCP(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	_ = os.WriteFile(a, []byte("line one\nline two\n"), 0o644)
	_ = os.WriteFile(b, []byte("line one\nline three\n"), 0o644)
	args, _ := json.Marshal(map[string]any{"root": root, "a": a, "b": b})
	resp := serveOne(t, writeReq("tools/call", 16, `{"name":"kern_diff_files","arguments":`+string(args)+`}`))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "line two") || !strings.Contains(out, "line three") {
		t.Fatalf("expected diff hunk, got %q", out)
	}
}

func TestLockUnlockViaMCP(t *testing.T) {
	root := t.TempDir()
	acq := `{"name":"kern_lock","arguments":` + jsonMust(map[string]any{"root": root, "scope": "build"}) + `}`
	rel := `{"name":"kern_unlock","arguments":` + jsonMust(map[string]any{"scope": "build"}) + `}`
	// Lock and unlock are dependent calls: submit one, wait for its response,
	// then submit the next (as a real client would).
	resps := serveSequential(t,
		writeReq("tools/call", 17, acq),
		writeReq("tools/call", 18, rel),
	)
	aout, _ := toolResultText(t, resps[0])
	if !strings.Contains(aout, "lock acquired") {
		t.Fatalf("lock acquire failed: %q", aout)
	}
	rout, _ := toolResultText(t, resps[1])
	if !strings.Contains(rout, "lock released") {
		t.Fatalf("lock release failed: %q", rout)
	}
}

// serveSequential feeds each request into the same running server and waits
// for its response before submitting the next. Tools run concurrently inside
// Serve, so dependent calls (lock -> unlock) must be sequenced by the client.
func serveSequential(t *testing.T, reqs ...string) []map[string]any {
	t.Helper()
	pr, pw := io.Pipe()
	out := &lockedBuffer{}
	s := NewServer(pr, out)
	// Tests use temp-dir roots outside the process cwd; confine to everything.
	s.roots = []string{"/"}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	var resps []map[string]any
	for _, req := range reqs {
		if _, err := io.WriteString(pw, req+"\n"); err != nil {
			t.Fatalf("write request: %v", err)
		}
		resps = append(resps, waitForResponse(t, out, req))
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return resps
}

// waitForResponse polls the output buffer until a response whose id matches
// the request's id appears, then returns it and clears the buffer.
func waitForResponse(t *testing.T, out *lockedBuffer, req string) map[string]any {
	t.Helper()
	var probe struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(req), &probe); err != nil {
		t.Fatalf("parse id from request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(out.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var r struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &r); err != nil || r.ID != probe.ID {
				continue
			}
			var full map[string]any
			if err := json.Unmarshal([]byte(line), &full); err != nil {
				t.Fatalf("unmarshal response %q: %v", line, err)
			}
			out.Reset()
			return full
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no response for %q", req)
	return nil
}

// lockedBuffer is a goroutine-safe buffer for a server that writes responses
// from tool goroutines while the test reads them.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func TestUnknownToolReturnsError(t *testing.T) {
	resp := serveOne(t, writeReq("tools/call", 19, `{"name":"kern_does_not_exist","arguments":{}}`))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "unknown tool") {
		t.Fatalf("expected isError for unknown tool: %+v", resp)
	}
}

func TestMissingPromptArg(t *testing.T) {
	resp := serveOne(t, writeReq("tools/call", 20, `{"name":"kern_optimize_prompt","arguments":{}}`))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "prompt") {
		t.Fatalf("expected missing-prompt isError: %+v", resp)
	}
}

func jsonMust(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestLockErrorIsNotFakeHeld verifies kern_lock reports the real Acquire
// failure instead of a fabricated "held (pid 0)" (W2-33).
func TestLockErrorIsNotFakeHeld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(root, []byte("file, not dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := `{"name":"kern_lock","arguments":` + jsonMust(map[string]any{"root": root, "scope": "build"}) + `}`
	resp := serveOne(t, writeReq("tools/call", 21, req))
	out, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("expected error for unwritable lock root, got %q", out)
	}
	if strings.Contains(out, "held (pid 0)") {
		t.Fatalf("must not fabricate a holder pid: %q", out)
	}
	if !strings.Contains(out, "lock scope is required") && !strings.Contains(out, "not a directory") {
		t.Fatalf("expected the real acquire error, got %q", out)
	}
}
