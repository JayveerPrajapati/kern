package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func toolsCallJSON(t *testing.T, id int, name string, args map[string]any) string {
	t.Helper()
	a, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return writeReq("tools/call", id, `{"name":"`+name+`","arguments":`+string(a)+`}`)
}

func TestSchemaValidateOK(t *testing.T) {
	args := map[string]any{"data": `{"name":"x","n":3}`, "schema": `{"type":"object","required":["name","n"],"properties":{"name":{"type":"string"},"n":{"type":"number"}}}`}
	resp := serveOne(t, toolsCallJSON(t, 31, "kern_schema_validate", args))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "schema OK") {
		t.Fatalf("expected conform message, got %q", out)
	}
}

func TestSchemaValidateViolations(t *testing.T) {
	args := map[string]any{"data": `{"name":123}`, "schema": `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`}
	resp := serveOne(t, toolsCallJSON(t, 32, "kern_schema_validate", args))
	out, _ := toolResultText(t, resp)
	if !strings.Contains(out, "schema violations") {
		t.Fatalf("expected violations, got %q", out)
	}
}

func TestSchemaValidateMissingArgs(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 33, "kern_schema_validate", map[string]any{}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "required") {
		t.Fatalf("expected missing-args isError, got: %+v", resp)
	}
}

func TestDiffFilesIdenticalAndMissing(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	if err := os.WriteFile(a, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := serveOne(t, toolsCallJSON(t, 34, "kern_diff_files", map[string]any{"root": root, "a": a, "b": b}))
	out, _ := toolResultText(t, resp)
	if out != "files identical" {
		t.Fatalf("expected identical, got %q", out)
	}

	resp = serveOne(t, toolsCallJSON(t, 35, "kern_diff_files", map[string]any{"root": root, "a": "", "b": ""}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "are required") {
		t.Fatalf("expected required-args isError, got: %+v", resp)
	}
}

func TestCompactFileMissingPath(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 36, "kern_compact_file", map[string]any{}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "path is required") {
		t.Fatalf("expected missing-path isError, got: %+v", resp)
	}
}

func TestVerifyOutputMissingText(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 37, "kern_verify_output", map[string]any{}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "text is required") {
		t.Fatalf("expected missing-text isError, got: %+v", resp)
	}
}

func TestPromptGetNotFound(t *testing.T) {
	resp := serveOne(t, writeReq("prompts/get", 38, `{"name":"does_not_exist","arguments":{}}`))
	if e, ok := resp["error"].(map[string]any); !ok || int(e["code"].(float64)) != -32602 {
		t.Fatalf("expected prompt-not-found (-32602), got: %+v", resp)
	}
}

func TestGraphCtxToolBudgeted(t *testing.T) {
	root := mcpProject(t)
	out := mcpAssertOK(t, "kern_graph", map[string]any{"root": root, "symbol": "Greet", "max_tokens": "150"})
	if !strings.Contains(out, "callers (1)") {
		t.Fatalf("expected caller-first adjacency with the caller listed, got %q", out)
	}
	if !strings.Contains(out, "[EXTRACTED]") {
		t.Fatalf("expected confidence tags, got %q", out)
	}
	if !strings.Contains(out, "community") {
		t.Fatalf("expected community membership, got %q", out)
	}
}

func TestGraphCtxToolUnknownSymbol(t *testing.T) {
	root := mcpProject(t)
	resp := serveOne(t, toolsCallJSON(t, 39, "kern_graph", map[string]any{"root": root, "symbol": "Nope"}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "unknown symbol") {
		t.Fatalf("expected unknown-symbol isError, got: %+v", resp)
	}
}

func TestGraphCtxToolMissingSymbol(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 40, "kern_graph", map[string]any{}))
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "symbol is required") {
		t.Fatalf("expected missing-symbol isError, got: %+v", resp)
	}
}

// mcpHubProject returns a project whose Hub symbol has 90 callers — far more
// than the fixed 400-token graphctx base could ever list.
func mcpHubProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("// Hub is called by ninety functions.\n")
	for i := 1; i <= 90; i++ {
		fmt.Fprintf(&b, "func hubCaller%02d() { Hub() }\n", i)
	}
	b.WriteString("func Hub() {}\n")
	if err := os.WriteFile(filepath.Join(root, "hub.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGraphCtxToolAdaptiveDefaultScalesUp pins the section-15-item-3 behavior
// change: kern_graph WITHOUT max_tokens no longer defaults to the fixed
// 400-token budget — it scales with the symbol's adjacency degree, so a
// 90-caller hub's last caller row survives. (The fixed-400 default would
// truncate it away.)
func TestGraphCtxToolAdaptiveDefaultScalesUp(t *testing.T) {
	root := mcpHubProject(t)
	out := mcpAssertOK(t, "kern_graph", map[string]any{"root": root, "symbol": "Hub"})
	if !strings.Contains(out, "callers (90):") {
		t.Fatalf("expected all 90 callers listed, got %q", out)
	}
	if !strings.Contains(out, "hubCaller90 [EXTRACTED]") {
		t.Fatalf("adaptive default must scale past the fixed 400-token base: last caller row missing, got %q", out)
	}
}

// TestGraphCtxToolExplicitBudgetWins: an explicit max_tokens must still be
// honored verbatim — with max_tokens=400 the same 90-caller hub is truncated
// and the last caller row is gone. (The adaptive default on the same fixture
// keeps it; the governor's filterGraphText rewrites the section header to the
// count of rows surviving the truncation, so the header read is not "90".)
func TestGraphCtxToolExplicitBudgetWins(t *testing.T) {
	root := mcpHubProject(t)
	out := mcpAssertOK(t, "kern_graph", map[string]any{"root": root, "symbol": "Hub", "max_tokens": "400"})
	if !strings.Contains(out, "callers (") {
		t.Fatalf("expected callers header, got %q", out)
	}
	if strings.Contains(out, "hubCaller90 [EXTRACTED]") {
		t.Fatalf("explicit max_tokens=400 must truncate the hub answer, got %q", out)
	}
	if strings.Contains(out, "callers (90):") {
		t.Fatalf("explicit max_tokens=400 must not list all 90 callers, got %q", out)
	}
}
