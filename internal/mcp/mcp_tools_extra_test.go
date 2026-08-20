package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

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
