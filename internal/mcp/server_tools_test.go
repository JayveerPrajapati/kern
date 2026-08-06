package mcp

import (
	"encoding/json"
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
	text, _ := toolResultText(t, resp)
	return text
}

func mcpToolError(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	resp := mcpCall(t, name, args)
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for %s, got: %+v", name, resp)
	}
	return e["message"].(string)
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
	ix1, err := loadOrBuildIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	ix2, err := loadOrBuildIndex(root)
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
	out = mcpAssertOK(t, "kern_entry_points", map[string]any{"root": root})
	if !strings.Contains(out, "no framework entry points") {
		t.Fatalf("expected entry-point message, got %q", out)
	}
	_ = mcpAssertOK(t, "kern_trace", map[string]any{"root": root, "trace": "panic in Greet\ncalled Greet\n", "limit": "10"})
	_ = mcpAssertOK(t, "kern_lock_status", map[string]any{"root": root})
	_ = mcpAssertOK(t, "kern_guard_check", map[string]any{"root": root, "file": "app.go"})
	_ = mcpAssertOK(t, "kern_path", map[string]any{"root": root, "from": "main", "to": "Greet"})
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

func TestMissingRequiredArgs(t *testing.T) {
	mcpToolError(t, "kern_ast_search", nil)
	mcpToolError(t, "kern_search", map[string]any{"root": "."})
	mcpToolError(t, "kern_path", map[string]any{"root": "."})
	mcpToolError(t, "kern_why", map[string]any{"root": "."})
	mcpToolError(t, "kern_near", map[string]any{"root": "."})
}
