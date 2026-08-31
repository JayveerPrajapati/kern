package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

func mcpProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\n// Greet says hello.\nfunc Greet() { println(\"hi\") }\n\n// helper is unused.\nfunc helper() {}\n\nfunc main() { Greet() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func mcpCall(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	pa, _ := json.Marshal(args)
	params := `{"name":"` + name + `","arguments":` + string(pa) + `}`
	return serveOne(t, writeReq("tools/call", name, params))
}

func mcpAssertOK(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	resp := mcpCall(t, name, args)
	if e, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("tool %s returned error: %+v", name, e)
	}
	text, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("tool %s returned isError result: %s", name, text)
	}
	return text
}

func mcpToolError(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	resp := mcpCall(t, name, args)
	text, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("expected isError for %s, got: %+v", name, resp)
	}
	return text
}

func TestHelpersItoaPctArgStringTruncate(t *testing.T) {
	if itoa(0) != "0" {
		t.Fatalf("itoa(0) = %q", itoa(0))
	}
	if itoa(42) != "42" {
		t.Fatalf("itoa(42) = %q", itoa(42))
	}
	if pct(100, 25) != 75 || pct(0, 1) != 0 {
		t.Fatal("pct wrong")
	}
	if argString(map[string]any{"k": " v "}, "k") != "v" {
		t.Fatal("argString trim failed")
	}
	if argString(map[string]any{"k": nil}, "k") != "" {
		t.Fatal("argString nil failed")
	}
	if argString(map[string]any{}, "missing") != "" {
		t.Fatal("argString missing failed")
	}
	if truncateMCP("abc", 10) != "abc" {
		t.Fatal("truncate should not trim short input")
	}
	long := strings.Repeat("x", 50)
	got := truncateMCP(long, 10)
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) || !strings.Contains(got, "... (truncated)") {
		t.Fatalf("truncate wrong: %q", got)
	}
}

func TestLoadOrBuildIndexCacheHit(t *testing.T) {
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	ix1, err := s.loadIndex(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	ix2, err := s.loadIndex(context.Background(), root)
	if err != nil || ix2 == nil {
		t.Fatalf("cache-hit index load failed: %v", err)
	}
	if len(ix2.Symbols) != len(ix1.Symbols) {
		t.Fatalf("cache-hit index mismatch: %d vs %d", len(ix2.Symbols), len(ix1.Symbols))
	}
}

func TestEnsureRecorderAndRenderStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := ensureRecorder(); err != nil {
		t.Fatal(err)
	}
	if optimize.Recorder == nil {
		t.Fatal("optimize.Recorder not wired by ensureRecorder")
	}
	out, err := renderStats("7", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "operations=") {
		t.Fatalf("bad stats render: %q", out)
	}
	if _, err := renderStats("notanumber", ""); err == nil {
		t.Fatal("expected error for invalid days")
	}
}

func TestChangedContextViaMCP(t *testing.T) {
	root := mcpProject(t)
	out := mcpAssertOK(t, "kern_changes", map[string]any{"root": root, "file": "app.go"})
	if !strings.Contains(out, "app.go") {
		t.Fatalf("expected changes mentioning app.go, got %q", out)
	}
}

func TestChangedContextRangeNeedsGit(t *testing.T) {
	root := mcpProject(t)
	_ = mcpToolError(t, "kern_changes", map[string]any{"root": root, "range": "HEAD~1..HEAD"})
}

func TestPathTraversalFileArgRejected(t *testing.T) {
	root := mcpProject(t)
	for _, evil := range []string{"../outside.go", "../../etc/passwd", root + "/../outside.go"} {
		out := mcpToolError(t, "kern_changes", map[string]any{"root": root, "file": evil})
		if !strings.Contains(out, "escapes project root") {
			t.Fatalf("file %q should be rejected, got %q", evil, out)
		}
	}
}

func TestRootedPathToolsRejectEscapes(t *testing.T) {
	root := mcpProject(t)
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	_ = os.WriteFile(outside, []byte("package main\n"), 0o644)

	// Declared root: escaping paths are rejected for both file tools.
	out := mcpToolError(t, "kern_diff_files", map[string]any{"root": root, "a": "../x.go", "b": "app.go"})
	if !strings.Contains(out, "escapes project root") {
		t.Fatalf("kern_diff_files should reject relative escape, got %q", out)
	}
	out = mcpToolError(t, "kern_compact_file", map[string]any{"root": root, "path": outside})
	if !strings.Contains(out, "escapes project root") {
		t.Fatalf("kern_compact_file should reject absolute path outside root, got %q", out)
	}

	// Inside the root the tools still work.
	out = mcpAssertOK(t, "kern_diff_files", map[string]any{"root": root, "a": "app.go", "b": "app.go"})
	if !strings.Contains(out, "files identical") {
		t.Fatalf("expected identical note, got %q", out)
	}
	out = mcpAssertOK(t, "kern_compact_file", map[string]any{"root": root, "path": "app.go"})
	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected app.go summary, got %q", out)
	}
}

// TestRootlessAbsolutePathRejected verifies that a rootless tool call may not
// target an arbitrary absolute path (e.g. path=/etc/shadow): the call must
// name a root so the path is confined to the workspace. Rootless relative
// paths still resolve against the current working directory.
func TestRootlessAbsolutePathRejected(t *testing.T) {
	root := mcpProject(t)
	msg := mcpToolError(t, "kern_compact_file", map[string]any{"path": filepath.Join(root, "app.go")})
	if !strings.Contains(msg, "absolute path requires root argument") {
		t.Fatalf("expected absolute-path rejection, got %q", msg)
	}
	// Providing a root confines the path to the workspace and still works.
	out := mcpAssertOK(t, "kern_compact_file", map[string]any{"root": root, "path": "app.go"})
	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected app.go summary with root, got %q", out)
	}
}

func TestGitBasedChurnAndRangeChanges(t *testing.T) {
	root := mcpProject(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	run("add", ".")
	run("commit", "-q", "-m", "init")

	churn := mcpAssertOK(t, "kern_churn", map[string]any{"root": root})
	if !strings.Contains(churn, "app.go") {
		t.Fatalf("expected churn to mention app.go, got %q", churn)
	}
	// Working-tree changes against HEAD (clean tree here -> clean summary or 0).
	_ = mcpAssertOK(t, "kern_changes", map[string]any{"root": root, "range": "HEAD"})
}

func TestCommitmsgToolInRealRepo(t *testing.T) {
	root := mcpProject(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	run("add", ".")
	run("commit", "-q", "-m", "init")

	// Stage a fix with a recognisable added line.
	if err := os.WriteFile(filepath.Join(root, "app.go"),
		[]byte("package app\n\nfunc main() { fix the crash by handling nil\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	out := mcpAssertOK(t, "kern_commitmsg", map[string]any{"root": root, "staged": "true"})
	if !strings.HasPrefix(out, "fix:") {
		t.Fatalf("expected fix: subject, got %q", out)
	}
	if !strings.Contains(out, "app.go") {
		t.Fatalf("expected body to mention app.go, got %q", out)
	}
}

func TestToolDispatchCoverage(t *testing.T) {
	root := mcpProject(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = mcpAssertOK(t, "kern_changes", map[string]any{"root": root, "file": "app.go"})
	_ = mcpAssertOK(t, "kern_review", map[string]any{"root": root, "file": "app.go", "max": "500"})
	_ = mcpAssertOK(t, "kern_test_gaps", map[string]any{"root": root})
	_ = mcpAssertOK(t, "kern_arch", map[string]any{"root": root})
	out := mcpAssertOK(t, "kern_dead", map[string]any{"root": root})
	if !strings.Contains(out, "helper") {
		t.Fatalf("expected dead code 'helper', got %q", out)
	}
	out = mcpAssertOK(t, "kern_larges", map[string]any{"root": root, "min_lines": "1"})
	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected Greet among large decls, got %q", out)
	}
	out = mcpAssertOK(t, "kern_hubs", map[string]any{"root": root})
	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected Greet as hub, got %q", out)
	}
	_ = mcpAssertOK(t, "kern_near", map[string]any{"root": root, "symbol": "Greet", "depth": "2", "max": "50"})
	_ = mcpAssertOK(t, "kern_walk", map[string]any{"root": root, "symbol": "Greet", "depth": "1"})
	_ = mcpAssertOK(t, "kern_code_graph", map[string]any{"root": root, "symbol": "Greet"})
	_ = mcpAssertOK(t, "kern_context", map[string]any{"root": root, "symbol": "Greet", "lines": "5"})
	out = mcpAssertOK(t, "kern_why", map[string]any{"root": root, "symbol": "Greet"})
	if !strings.Contains(out, "Greet says hello") {
		t.Fatalf("expected doc for Greet, got %q", out)
	}
	out = mcpAssertOK(t, "kern_search", map[string]any{"root": root, "query": "greet"})
	if !strings.Contains(strings.ToLower(out), "greet") {
		t.Fatalf("expected search hit, got %q", out)
	}
	_ = mcpAssertOK(t, "kern_inherits", map[string]any{"root": root, "symbol": "Greet"})
	out = mcpAssertOK(t, "kern_entry_points", map[string]any{"root": root})
	if !strings.Contains(out, "no framework entry points") {
		t.Fatalf("expected entry-point message, got %q", out)
	}
	_ = mcpAssertOK(t, "kern_trace", map[string]any{"root": root, "trace": "panic in Greet\ncalled Greet\n", "limit": "10"})
	_ = mcpAssertOK(t, "kern_lock_status", map[string]any{"root": root})
	_ = mcpAssertOK(t, "kern_guard_check", map[string]any{"root": root, "file": "app.go"})
	_ = mcpAssertOK(t, "kern_path", map[string]any{"root": root, "from": "main", "to": "Greet"})
}

func TestInheritsToolViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package main

type Reader interface{ Read() int }

type Logger interface {
	Reader
	Log(msg string)
}

type Base struct{ ID int }

type Item struct {
	Base
	Logger
}
`
	if err := os.WriteFile(filepath.Join(root, "types.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mcpAssertOK(t, "kern_inherits", map[string]any{"root": root, "symbol": "Logger"})
	if !strings.Contains(out, "embeds:Reader") || !strings.Contains(out, "Item") {
		t.Fatalf("expected Logger supertype embeds:Reader and subtype Item, got %q", out)
	}
	out = mcpAssertOK(t, "kern_inherits", map[string]any{"root": root, "symbol": "Item"})
	if !strings.Contains(out, "embeds:Base") || !strings.Contains(out, "embeds:Logger") {
		t.Fatalf("expected Item supertypes Base+Logger, got %q", out)
	}
}

func TestTestGapsHonorsLimit(t *testing.T) {
	root := mcpProject(t)
	src := "package main\n\n// A is uncovered but called.\nfunc A() {}\nfunc B() { A() }\nfunc C() { A() }\nfunc D() { A() }\n\nfunc main() { B(); C(); D() }\n"
	if err := os.WriteFile(filepath.Join(root, "hot.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	full := mcpAssertOK(t, "kern_test_gaps", map[string]any{"root": root})
	limited := mcpAssertOK(t, "kern_test_gaps", map[string]any{"root": root, "limit": "1"})
	if strings.Count(full, "(0 callers)") == 0 && !strings.Contains(full, "A ") {
		t.Fatalf("expected hotspot A in full output, got %q", full)
	}
	if strings.Contains(limited, "B ") || strings.Contains(limited, "C ") {
		t.Fatalf("limit=1 should only list the top hotspot, got %q", limited)
	}
}

func TestPromptGetViaMCP(t *testing.T) {
	params := `{"name":"review_changes","arguments":{}}`
	resp := serveOne(t, writeReq("prompts/get", 42, params))
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %+v", resp)
	}
	if !strings.Contains(res["description"].(string), "Pre-commit") {
		t.Fatalf("bad prompt description: %v", res["description"])
	}
	msgs := res["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("expected a prompt message")
	}
}

func TestKernStatsViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = ensureRecorder()
	mcpAssertOK(t, "kern_stats", nil)
}

func TestSecurityToolViaMCP(t *testing.T) {
	root := mcpProject(t)
	secret := "const apiKey = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\"\n"
	if err := os.WriteFile(filepath.Join(root, "creds.go"), []byte("package main\n\n"+secret), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mcpAssertOK(t, "kern_security", map[string]any{"root": root})
	if !strings.Contains(out, "hardcoded-secret") || !strings.Contains(out, "creds.go:3") {
		t.Fatalf("expected hardcoded-secret finding, got %q", out)
	}
	if !strings.Contains(out, "[kern]") {
		t.Fatalf("expected summary line, got %q", out)
	}
	// severity filter drops the error finding.
	warnOnly := mcpAssertOK(t, "kern_security", map[string]any{"root": root, "severity": "warning"})
	if strings.Contains(warnOnly, "hardcoded-secret") {
		t.Fatalf("severity filter should exclude errors, got %q", warnOnly)
	}
	// JSON format returns parseable output.
	jsonOut := mcpAssertOK(t, "kern_security", map[string]any{"root": root, "format": "json", "max": "0"})
	var findings []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &findings); err != nil {
		t.Fatalf("expected JSON findings, got %q: %v", jsonOut, err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one JSON finding")
	}
}

func TestSafeDeleteToolViaMCP(t *testing.T) {
	root := mcpProject(t)
	// mcpProject defines helper (unused) and Greet (called by main).
	safe := mcpAssertOK(t, "kern_safe_delete", map[string]any{"root": root, "symbol": "helper"})
	if !strings.Contains(safe, "SAFE") {
		t.Fatalf("helper is unused and private, expected SAFE, got %q", safe)
	}
	unsafe := mcpAssertOK(t, "kern_safe_delete", map[string]any{"root": root, "symbol": "Greet"})
	if !strings.Contains(unsafe, "NOT SAFE") {
		t.Fatalf("Greet is exported and called by main, expected NOT SAFE, got %q", unsafe)
	}
	jsonOut := mcpAssertOK(t, "kern_safe_delete", map[string]any{"root": root, "symbol": "Greet", "format": "json"})
	if !strings.Contains(jsonOut, `"safe":false`) {
		t.Fatalf("expected json report, got %q", jsonOut)
	}
	mcpToolError(t, "kern_safe_delete", map[string]any{"root": root})
}

func TestMissingRequiredArgs(t *testing.T) {
	mcpToolError(t, "kern_ast_search", nil)
	mcpToolError(t, "kern_search", map[string]any{"root": "."})
	mcpToolError(t, "kern_path", map[string]any{"root": "."})
	mcpToolError(t, "kern_why", map[string]any{"root": "."})
}

func runToolCases() []string {
	src, err := os.ReadFile("server.go")
	if err != nil {
		panic(err)
	}
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `case "kern_`) {
			continue
		}
		for _, name := range strings.Split(strings.TrimSuffix(line[len(`case "`):], `":`), `", "`) {
			out = append(out, name)
		}
	}
	return out
}

func TestMemoryToolsViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)

	ok := mcpAssertOK(t, "kern_memory_add", map[string]any{"root": root, "lesson": "deploy tags come from a manual release workflow"})
	if !strings.Contains(ok, "remembered") {
		t.Fatalf("expected remembered confirmation, got %q", ok)
	}
	full := mcpAssertOK(t, "kern_memory_list", map[string]any{"root": root})
	if !strings.Contains(full, "deploy tags") {
		t.Fatalf("expected lesson in list, got %q", full)
	}
	hit := mcpAssertOK(t, "kern_memory_recall", map[string]any{"root": root, "prompt": "how are deploy tags released?", "k": "3"})
	if !strings.Contains(hit, "deploy tags") {
		t.Fatalf("expected recall hit, got %q", hit)
	}
	miss := mcpAssertOK(t, "kern_memory_recall", map[string]any{"root": root, "prompt": "xyzzy plugh unrelated"})
	if miss != "" {
		t.Fatalf("expected empty recall for unrelated prompt, got %q", miss)
	}
	badK := mcpToolError(t, "kern_memory_recall", map[string]any{"root": root, "prompt": "how are deploy tags released?", "k": "bogus"})
	if !strings.Contains(badK, "invalid integer") {
		t.Fatalf("expected parse error for malformed k, got %q", badK)
	}
	zeroK := mcpAssertOK(t, "kern_memory_recall", map[string]any{"root": root, "prompt": "how are deploy tags released?", "k": "0"})
	if !strings.Contains(zeroK, "deploy tags") {
		t.Fatalf("expected recall hit with clamped k, got %q", zeroK)
	}
	mcpToolError(t, "kern_memory_add", nil)
	mcpToolError(t, "kern_memory_recall", nil)
}

func TestToolsListMatchesDispatchCases(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range runToolCases() {
		seen[c] = true
	}
	for _, tool := range tools {
		if !seen[tool.Name] {
			t.Errorf("tool %s has no runTool case", tool.Name)
		}
	}
}

func TestToolsListContainsWalk(t *testing.T) {
	// kern_walk is not in the default minimal surface; opt in to the full
	// catalog so the wire response advertises it.
	t.Setenv("KERN_MCP_FULL", "1")
	resp := serveOne(t, writeReq("tools/list", 3, ``))
	res := resp["result"].(map[string]any)
	for _, item := range res["tools"].([]any) {
		name := item.(map[string]any)["name"].(string)
		if name == "kern_walk" {
			return
		}
	}
	t.Fatal("kern_walk missing from tools/list")
}

func TestProvenanceStampOnIndexTools(t *testing.T) {
	root := mcpProject(t)
	out := mcpAssertOK(t, "kern_search", map[string]any{"root": root, "query": "greet"})
	if !strings.Contains(out, "[kern] index: ") {
		t.Fatalf("expected provenance stamp on index tool, got %q", out)
	}
	if !strings.Contains(out, "symbols") || !strings.Contains(out, "call edges") ||
		!strings.Contains(out, "packages") || !strings.Contains(out, "fresh") {
		t.Fatalf("provenance stamp missing fields, got %q", out)
	}
}

func TestNoProvenanceStampOnNonIndexTools(t *testing.T) {
	root := mcpProject(t)
	_ = mcpAssertOK(t, "kern_memory_add", map[string]any{"root": root, "lesson": "provenance probe"})
	out := mcpAssertOK(t, "kern_memory_list", map[string]any{"root": root})
	if strings.Contains(out, "[kern] index: ") {
		t.Fatalf("non-index tool should not be stamped, got %q", out)
	}
}

// guardProject writes a small two-package tree with a client->lib forbid rule.
func guardProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	files := map[string]string{
		"lib/lib.go":       "package lib\n\nfunc Public() {}\n",
		"client/client.go": "package client\n\nimport \"lib\"\n\nfunc Touch() { lib.Public() }\n",
		".kern/boundaries.json": `{
  "description": "no client to lib",
  "rules": [
    {"from": "client", "to": "lib", "action": "forbid"}
  ]
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGuardCheckSARIFFormat(t *testing.T) {
	root := guardProject(t)
	out := mcpAssertOK(t, "kern_guard_check", map[string]any{
		"root": root, "file": "client/client.go", "format": "sarif", "threshold": "5",
	})
	if !strings.Contains(out, `"$schema"`) || !strings.Contains(out, `"version": "2.1.0"`) {
		t.Fatalf("expected SARIF 2.1.0 document, got %q", out)
	}
	if !strings.Contains(out, "kern/boundary/forbid/client/lib") {
		t.Fatalf("expected boundary rule id in SARIF, got %q", out)
	}
	if !strings.Contains(out, "client/client.go") {
		t.Fatalf("expected caller artifact uri in SARIF, got %q", out)
	}
}

func TestGuardCheckThresholdGates(t *testing.T) {
	root := guardProject(t)
	// Default threshold 0: any violation fails the call.
	err := mcpToolError(t, "kern_guard_check", map[string]any{"root": root, "file": "client/client.go"})
	if !strings.Contains(err, "exceed threshold 0") {
		t.Fatalf("expected threshold rejection, got %q", err)
	}
	// threshold=2 permits the single violation.
	out := mcpAssertOK(t, "kern_guard_check", map[string]any{
		"root": root, "file": "client/client.go", "threshold": "2",
	})
	if !strings.Contains(out, "REJECT") {
		t.Fatalf("expected REJECT verdict text, got %q", out)
	}
	// threshold=1 also permits exactly one violation (fail only when count > N).
	_ = mcpAssertOK(t, "kern_guard_check", map[string]any{
		"root": root, "file": "client/client.go", "threshold": "1",
	})
}

func TestRenamePreviewAndApply(t *testing.T) {
	root := mcpProject(t)
	// Preview mode: reports the definition + the reference in main(), does not write.
	out := mcpAssertOK(t, "kern_rename", map[string]any{"root": root, "symbol": "Greet", "new_name": "Hi"})
	if !strings.Contains(out, "PREVIEW") || !strings.Contains(out, "Greet -> Hi") {
		t.Fatalf("expected rename preview, got %q", out)
	}
	if !strings.Contains(out, "app.go") || !strings.Contains(out, "definition") {
		t.Fatalf("expected file:line:col edits in preview, got %q", out)
	}
	// The index is rebuilt lazily from hashes; nothing was written yet.
	if !strings.Contains(out, "--apply") {
		t.Fatalf("preview should hint at --apply, got %q", out)
	}

	// Apply mode: commits and reports a backup path.
	out = mcpAssertOK(t, "kern_rename", map[string]any{"root": root, "symbol": "Greet", "new_name": "Hi", "apply": "true"})
	if !strings.Contains(out, "renamed Greet -> Hi") {
		t.Fatalf("expected applied report, got %q", out)
	}
	if !strings.Contains(out, ".kern/rename-backup") {
		t.Fatalf("expected backup path, got %q", out)
	}
	live, err := os.ReadFile(filepath.Join(root, "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "func Hi()") || strings.Contains(string(live), "Greet()") {
		t.Fatalf("rename not reflected in source: %s", live)
	}

	// Missing symbol and invalid new name are errors, not silent no-ops.
	err1 := mcpToolError(t, "kern_rename", map[string]any{"root": root, "symbol": "Nope", "new_name": "X"})
	if !strings.Contains(err1, "not found") {
		t.Fatalf("expected not-found error, got %q", err1)
	}
	err2 := mcpToolError(t, "kern_rename", map[string]any{"root": root, "symbol": "Hi", "new_name": "for"})
	if !strings.Contains(err2, "not a valid Go identifier") {
		t.Fatalf("expected identifier error, got %q", err2)
	}
}

func TestRenameRefusesMethodAndNonGo(t *testing.T) {
	root := mcpProject(t)
	// v2: method-form names ("main.Greet" = receiver main, method Greet) are
	// handled by the method path — with no receiver type "main" in the
	// fixture, the result is a not-found error, never a guessed edit.
	err := mcpToolError(t, "kern_rename", map[string]any{"root": root, "symbol": "main.Greet", "new_name": "Hi"})
	if !strings.Contains(err, "not found") {
		t.Fatalf("expected not-found for method-form name with unknown receiver, got %q", err)
	}
}

func TestExecReturnsOnlyStdout(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	// kern_exec is a governed exec surface and runs scripts in network
	// isolation (which fails closed when netns is unavailable). Opt in to both
	// so the test exercises the real execution path on any host.
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_NET", "1")
	// Success: stdout only, no stderr, no stats noise.
	out := mcpAssertOK(t, "kern_exec", map[string]any{
		"code": "print(6*7)\nimport sys\nprint('noise', file=sys.stderr)\n",
		"lang": "python3",
	})
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("expected clean stdout '42', got %q", out)
	}
	if strings.Contains(out, "noise") || strings.Contains(out, "kern_exec") {
		t.Fatalf("stdout must be pure script output, got %q", out)
	}

	// Stdin is forwarded.
	out = mcpAssertOK(t, "kern_exec", map[string]any{
		"code":  "import sys\nprint(sys.stdin.read().upper().strip())",
		"lang":  "python3",
		"stdin": "abc",
	})
	if strings.TrimSpace(out) != "ABC" {
		t.Fatalf("stdin forwarding failed, got %q", out)
	}

	// Failure: non-zero exit surfaces the error (with stderr).
	err := mcpToolError(t, "kern_exec", map[string]any{
		"code": "print(1/0)",
		"lang": "python3",
	})
	if !strings.Contains(err, "exit code 1") || !strings.Contains(err, "ZeroDivisionError") {
		t.Fatalf("expected failure with stderr, got %q", err)
	}

	// Missing runtime / unknown lang are clear errors.
	if _, err := exec.LookPath("cobol-thing"); err != nil {
		err := mcpToolError(t, "kern_exec", map[string]any{"code": "x", "lang": "cobol-thing"})
		if !strings.Contains(err, "unknown language") {
			t.Fatalf("expected unknown-language error, got %q", err)
		}
	}

	// Shebang detection works through the MCP surface.
	out = mcpAssertOK(t, "kern_exec", map[string]any{
		"code": "#!/usr/bin/env bash\necho hi\n",
	})
	if strings.TrimSpace(out) != "hi" {
		t.Fatalf("shebang detection failed, got %q", out)
	}
}

func TestOutputSandboxUnit(t *testing.T) {
	big := strings.Repeat("lorem ipsum dolor sit amet ", 2000)
	// Under budget: untouched.
	if got := sandboxOutput(big, 1<<20, "kern_project_map"); got != big {
		t.Fatalf("under-budget output was modified")
	}
	// Over budget: truncated with marker + token counts + tool hint.
	got := sandboxOutput(big, 200, "kern_project_map")
	if !strings.Contains(got, "MCP output sandbox") || !strings.Contains(got, "tokens") {
		t.Fatalf("missing sandbox marker: %q", got)
	}
	if !strings.Contains(got, "kern_context") {
		t.Fatalf("missing recovery hint: %q", got)
	}
	if len(got) > 500 {
		t.Fatalf("sandboxed output too large: %d", len(got))
	}
	// budget <= 0 disables.
	if got := sandboxOutput(big, 0, "kern_x"); got != big {
		t.Fatalf("budget 0 should disable the sandbox")
	}
}

func TestOutputBudgetResolution(t *testing.T) {
	// Per-call max_output wins over the global cap.
	if b, err := callOutputBudget(map[string]any{"max_output": "500"}); err != nil || b != 500 {
		t.Fatalf("max_output override = %d, err=%v", b, err)
	}
	if b, err := callOutputBudget(map[string]any{"max_output": "0"}); err != nil || b != 0 {
		t.Fatalf("max_output=0 should disable, got %d, err=%v", b, err)
	}
	// A malformed max_output is an error, not a silent fallback.
	if _, err := callOutputBudget(map[string]any{"max_output": "junk"}); err == nil {
		t.Fatalf("malformed max_output should error")
	}
	// Global env cap applies when no per-call override.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "999")
	if b, err := callOutputBudget(map[string]any{}); err != nil || b != 999 {
		t.Fatalf("env cap = %d, err=%v", b, err)
	}
	if b := outputBudget(); b != 999 {
		t.Fatalf("outputBudget = %d", b)
	}
	// Invalid env falls back to the default.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "junk")
	if b := outputBudget(); b != defaultOutputBudget {
		t.Fatalf("invalid env should fall back to default, got %d", b)
	}
}

func TestAtoiArgReportsParseErrors(t *testing.T) {
	// Empty input keeps the default.
	if n, err := atoiArg("", 42); err != nil || n != 42 {
		t.Fatalf("empty -> %d, %v; want 42, nil", n, err)
	}
	// Valid integers parse.
	if n, err := atoiArg("7", 42); err != nil || n != 7 {
		t.Fatalf("7 -> %d, %v; want 7, nil", n, err)
	}
	// A malformed value is an error, not a silent default.
	if _, err := atoiArg("junk", 42); err == nil {
		t.Fatal("malformed integer must error, not fall back silently")
	}
}

func TestOutputSandboxThroughMCPChokepoint(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	// Governed exec surface + network-isolation opt-in (see TestExecReturnsOnlyStdout).
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_NET", "1")
	// A tiny global cap forces every tool result through the sandbox.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "128")
	out := mcpAssertOK(t, "kern_exec", map[string]any{
		"code": "print('x'*400)",
		"lang": "python3",
	})
	if !strings.Contains(out, "MCP output sandbox") {
		t.Fatalf("expected sandbox marker in tool output, got %q", out)
	}
	if len(out) > 600 {
		t.Fatalf("sandboxed output too large: %d", len(out))
	}
	// An explicit max_output=0 disables the sandbox for that call.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "128")
	out = mcpAssertOK(t, "kern_exec", map[string]any{
		"code":       "print('x'*400)",
		"lang":       "python3",
		"max_output": "0",
	})
	if strings.Contains(out, "MCP output sandbox") {
		t.Fatalf("max_output=0 should bypass the sandbox, got %q", out)
	}
}

func packProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# rules\nUse pack.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Greet() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "util"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "util/util.go"), []byte("package util\n\nvar N = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPackThroughMCP(t *testing.T) {
	root := packProject(t)
	out := mcpAssertOK(t, "kern_pack", map[string]any{"root": root, "max_tokens": "5000"})
	for _, marker := range []string{"INSTRUCTIONS", "REPOSITORY STRUCTURE", "REPOSITORY FILES", "STATS", "## AGENTS.md", "Use pack.", "## File: main.go", "func Greet", "util/util.go"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("pack missing %q", marker)
		}
	}
	// A tiny budget drops files and says so.
	out = mcpAssertOK(t, "kern_pack", map[string]any{"root": root, "max_tokens": "30"})
	if !strings.Contains(out, "Dropped to fit budget") {
		t.Fatalf("tiny-budget pack should report dropped files, got %q", out)
	}

	// JSON format is machine-readable.
	js := mcpAssertOK(t, "kern_pack", map[string]any{"root": root, "format": "json", "instructions": "false", "max_tokens": "5000"})
	for _, frag := range []string{`"root"`, `"files"`, `"instructions"`, `"content"`} {
		if !strings.Contains(js, frag) {
			t.Fatalf("pack json missing %q", frag)
		}
	}
}

func TestPackSandboxOverrideThroughMCP(t *testing.T) {
	root := packProject(t)
	// A tiny output sandbox would truncate the pack; max_output=0 bypasses it.
	t.Setenv("KERN_MCP_MAX_OUTPUT", "128")
	out := mcpAssertOK(t, "kern_pack", map[string]any{"root": root, "max_tokens": "200", "max_output": "0"})
	if strings.Contains(out, "MCP output sandbox") {
		t.Fatalf("max_output=0 should return the full pack, got %q", out)
	}
	if !strings.Contains(out, "REPOSITORY FILES") {
		t.Fatalf("expected full pack, got %q", out)
	}
}

func TestKernToolsAllowlistFiltersListAndCalls(t *testing.T) {
	// Full catalog so the allowlist intersects the complete 84-tool set
	// (kern_usage_guide is not part of the default minimal surface).
	t.Setenv("KERN_MCP_FULL", "1")
	t.Setenv("KERN_TOOLS", "kern_search, kern_usage_guide")
	s := NewServer(strings.NewReader(""), io.Discard)
	filtered := s.filteredTools()
	if len(filtered) != 2 {
		t.Fatalf("KERN_TOOLS should expose exactly 2 tools, got %d: %v", len(filtered), toolNamesOf(filtered))
	}
	for _, want := range []string{"kern_search", "kern_usage_guide"} {
		if !containsTool(filtered, want) {
			t.Errorf("filtered list missing %s", want)
		}
	}

	// tools/list over the wire honors the allowlist too.
	resp := serveOne(t, writeReq("tools/list", 3, ``))
	res := resp["result"].(map[string]any)
	if got := len(res["tools"].([]any)); got != 2 {
		t.Errorf("tools/list should return 2 tools under KERN_TOOLS, got %d", got)
	}

	// An allowlisted tool still runs.
	_ = mcpAssertOK(t, "kern_search", map[string]any{"root": mcpProject(t), "query": "Greet"})

	// A non-allowlisted tool is rejected.
	errText := mcpToolError(t, "kern_dead", map[string]any{"root": t.TempDir()})
	if !strings.Contains(errText, "not allowed") {
		t.Errorf("expected not-allowed error, got %q", errText)
	}
}

func TestKernToolsAllowlistEmptyAllowsAll(t *testing.T) {
	// The point is that an empty allowlist restricts nothing, so the full
	// catalog must be exposed under KERN_MCP_FULL=1.
	t.Setenv("KERN_MCP_FULL", "1")
	t.Setenv("KERN_TOOLS", " ")
	s := NewServer(strings.NewReader(""), io.Discard)
	if got := len(s.filteredTools()); got != len(tools) {
		t.Errorf("empty allowlist should expose all %d tools, got %d", len(tools), got)
	}
}

func TestKernMCPHighLevelOnlyFiltersTools(t *testing.T) {
	// A server is created after setting the env var, so tools/list honors it.
	t.Setenv("KERN_MCP_HIGH_LEVEL_ONLY", "1")
	resp := serveOne(t, writeReq("tools/list", 3, ``))
	res := resp["result"].(map[string]any)
	listed := res["tools"].([]any)
	if len(listed) == 0 {
		t.Fatal("tools/list returned no tools under KERN_MCP_HIGH_LEVEL_ONLY")
	}
	names := make(map[string]bool, len(listed))
	for _, t := range listed {
		names[t.(map[string]any)["name"].(string)] = true
	}
	// Only tools in highLevelTools are registered.
	for name := range names {
		if !highLevelTools[name] {
			t.Errorf("tool %q should be filtered out under KERN_MCP_HIGH_LEVEL_ONLY", name)
		}
	}
	// Tools known to be low-level must be absent.
	for _, low := range []string{"kern_dead", "kern_rename", "kern_churn", "kern_optimize_output"} {
		if names[low] {
			t.Errorf("low-level tool %q should be filtered out under KEN_MCP_HIGH_LEVEL_ONLY", low)
		}
	}
	// High-level orchestration tools must be present.
	for _, high := range []string{"kern_analyze", "kern_plan", "kern_execute", "kern_verify", "kern_incident"} {
		if !names[high] {
			t.Errorf("high-level tool %q should be present under KEN_MCP_HIGH_LEVEL_ONLY", high)
		}
	}
}

func toolNamesOf(ts []Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func containsTool(ts []Tool, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}
