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
	if e, ok := resp["error"].(map[string]any); !ok || !strings.Contains(e["message"].(string), "required") {
		t.Fatalf("expected missing-args error, got: %+v", resp)
	}
}

func TestDiffFilesIdenticalAndMissing(t *testing.T) {
	a := tmpFile(t, "a.txt", "same\n")
	b := tmpFile(t, "b.txt", "same\n")
	resp := serveOne(t, toolsCallJSON(t, 34, "kern_diff_files", map[string]any{"a": a, "b": b}))
	out, _ := toolResultText(t, resp)
	if out != "files identical" {
		t.Fatalf("expected identical, got %q", out)
	}

	resp = serveOne(t, toolsCallJSON(t, 35, "kern_diff_files", map[string]any{"a": "", "b": ""}))
	if e, ok := resp["error"].(map[string]any); !ok || !strings.Contains(e["message"].(string), "are required") {
		t.Fatalf("expected required-args error, got: %+v", resp)
	}
}

func TestCompactFileMissingPath(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 36, "kern_compact_file", map[string]any{}))
	if e, ok := resp["error"].(map[string]any); !ok || !strings.Contains(e["message"].(string), "path is required") {
		t.Fatalf("expected missing-path error, got: %+v", resp)
	}
}

func TestVerifyOutputMissingText(t *testing.T) {
	resp := serveOne(t, toolsCallJSON(t, 37, "kern_verify_output", map[string]any{}))
	if e, ok := resp["error"].(map[string]any); !ok || !strings.Contains(e["message"].(string), "text is required") {
		t.Fatalf("expected missing-text error, got: %+v", resp)
	}
}

func TestPromptGetNotFound(t *testing.T) {
	resp := serveOne(t, writeReq("prompts/get", 38, `{"name":"does_not_exist","arguments":{}}`))
	if e, ok := resp["error"].(map[string]any); !ok || int(e["code"].(float64)) != -32002 {
		t.Fatalf("expected prompt-not-found (-32002), got: %+v", resp)
	}
}
