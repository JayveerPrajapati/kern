package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain-level KERN_MCP_AUDIT_DIR isolation lives in zz_testmain_test.go;
// these tests override it per-case with t.Setenv for their own assertions.

// readAuditEntries reads every persisted audit entry from the audit dir,
// decoding both on-disk formats: legacy per-key <key>.json files and the
// {"k":...,"v":...} lines of chain.jsonl (LogStore).
func readAuditEntries(t *testing.T) []map[string]any {
	t.Helper()
	dir := os.Getenv("KERN_MCP_AUDIT_DIR")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read audit dir: %v", err)
	}
	var out []map[string]any
	for _, f := range files {
		name := f.Name()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read audit entry: %v", err)
		}
		switch {
		case strings.HasSuffix(name, ".json"):
			// Legacy per-key file: the whole document is one entry.
			var e map[string]any
			if json.Unmarshal(data, &e) == nil {
				out = append(out, e)
			}
		case name == "chain.jsonl":
			// Chain format: one {"k":...,"v":...} record per line.
			for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
				var rec struct {
					K string          `json:"k"`
					V json.RawMessage `json:"v"`
				}
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					continue
				}
				var e map[string]any
				if json.Unmarshal(rec.V, &e) == nil {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

// TestToolCallsAudited: an executed read-only tool call appends a tool_call
// entry (Action/Resource/Result) to the persisted audit chain.
func TestToolCallsAudited(t *testing.T) {
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	root := mcpProject(t)
	_ = mcpAssertOK(t, "kern_search", map[string]any{"root": root, "query": "greet"})
	found := false
	for _, e := range readAuditEntries(t) {
		if e["Action"] == "tool_call" && e["Resource"] == "kern_search" && e["Result"] == "allowed" && e["AgentID"] == "default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an allowed tool_call audit entry for kern_search, got %+v", readAuditEntries(t))
	}
}

// TestAuditToolCallBlockedResult: pre-dispatch rejections are recorded as
// blocked with Approved=false, not allowed/error.
func TestAuditToolCallBlockedResult(t *testing.T) {
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.auditToolCall("kern_arch", nil, nil, false)
	entries := readAuditEntries(t)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e["Action"] != "tool_call" || e["Resource"] != "kern_arch" || e["Result"] != "blocked" || e["Approved"] != false {
		t.Fatalf("blocked entry shape wrong: %+v", e)
	}
}

// TestAuditToolCallNilArgsSafe: nil args (no agent_id, no task) must not
// panic and must default the agent identity.
func TestAuditToolCallNilArgsSafe(t *testing.T) {
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.auditToolCall("kern_search", nil, nil, true)
	for _, e := range readAuditEntries(t) {
		if e["AgentID"] != "default" {
			t.Fatalf("expected default agent identity, got %+v", e)
		}
	}
}
