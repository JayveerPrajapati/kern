package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// TestToolCallRecordsPerToolEntry verifies the dispatch path appends one
// per-tool stats entry per executed tool call: tool name, session and the
// returned payload token estimate (AfterTokens > 0). Recording must never
// fail the call itself.
func TestToolCallRecordsPerToolEntry(t *testing.T) {
	root := mcpProject(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")
	lruReset()
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	s.gate = nil
	s.preTool = nil

	// Re-wire the shared recorder to this test's cache dir so the dispatch
	// path records where we look.
	if err := optimize.EnsureRecorder(); err != nil {
		t.Fatalf("EnsureRecorder: %v", err)
	}
	args := map[string]any{"root": root, "query": "Greet", "session": "sess-1"}
	pa, _ := json.Marshal(map[string]any{"name": "kern_search", "arguments": args})
	resp := s.toolCallResponse(json.RawMessage(`"1"`), pa).(map[string]any)
	if isErrorResult(resp) {
		t.Fatalf("kern_search errored: %q", contentText(resp))
	}

	rec, err := stats.NewRecorder()
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	es, err := rec.Entries(100)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var found *stats.Entry
	for i := range es {
		if es[i].Tool == "kern_search" {
			found = &es[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no per-tool entry for kern_search in stats (%d entries)", len(es))
	}
	if found.Operation != stats.OpToolCall {
		t.Fatalf("expected operation %q, got %q", stats.OpToolCall, found.Operation)
	}
	if found.AfterTokens <= 0 {
		t.Fatalf("expected positive returned-token estimate, got %d", found.AfterTokens)
	}
	if found.SavedTokens != 0 || found.CostSavedUSD != 0 {
		t.Fatalf("tools without known savings must record zero savings, got %+v", found)
	}
	if found.Session != "sess-1" {
		t.Fatalf("session not stamped, got %q", found.Session)
	}
	if found.Agent != "mcp" {
		t.Errorf("no initialize / no agent_id must fall back to the neutral %q bucket, got %q", "mcp", found.Agent)
	}
}

// TestRecordToolCallSkipsSelfRecording verifies the dispatch path does not
// double-record tools whose handlers already append a real-savings entry
// (optimize family) — one dispatch call must yield exactly one ledger row
// for the tool.
func TestRecordToolCallSkipsSelfRecording(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Always re-wire the shared recorder: it is process-global, so a prior
	// test's recorder would otherwise keep writing to its own cache dir.
	if err := optimize.EnsureRecorder(); err != nil {
		t.Fatalf("EnsureRecorder: %v", err)
	}
	recordToolCall("kern_search", nil, "a returned payload", "")
	recordToolCall("kern_optimize_prompt", nil, "must not be recorded at dispatch", "")

	rec, err := stats.NewRecorder()
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	es, err := rec.Entries(100)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(es) != 1 {
		t.Fatalf("expected exactly 1 dispatch entry, got %d: %+v", len(es), es)
	}
	if es[0].Tool != "kern_search" {
		t.Fatalf("expected kern_search entry, got %+v", es[0])
	}
}

// TestRenderStatsByTool verifies the kern_stats MCP by_tool mode renders the
// per-tool table from the recorded ledger.
func TestRenderStatsByTool(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := optimize.EnsureRecorder(); err != nil {
		t.Fatalf("EnsureRecorder: %v", err)
	}
	recordToolCall("kern_search", nil, "hello world payload for the ledger", "")

	out, err := renderStatsByTool("", "")
	if err != nil {
		t.Fatalf("renderStatsByTool: %v", err)
	}
	if !strings.Contains(out, "kern_search") || !strings.Contains(out, "calls") {
		t.Fatalf("by-tool table missing kern_search row:\n%s", out)
	}
	if strings.Contains(out, "no per-tool data") {
		t.Fatalf("expected data, got empty table:\n%s", out)
	}
}

// TestToolCallRecordsClientNameAttribution verifies the N-followup agent
// attribution: a tool call after an initialize that announced clientInfo.name
// records that name as the entry's Agent; an explicit agent_id argument still
// wins over the captured client name.
func TestToolCallRecordsClientNameAttribution(t *testing.T) {
	root := mcpProject(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_MCP_AUDIT_DIR", t.TempDir())
	t.Setenv("KERN_PRELOAD", "0")
	lruReset()
	if err := optimize.EnsureRecorder(); err != nil {
		t.Fatalf("EnsureRecorder: %v", err)
	}
	init := writeReq("initialize", 1, `{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"opencode","version":"1.0"}}`)
	args1, _ := json.Marshal(map[string]any{"name": "kern_search", "arguments": map[string]any{"root": root, "query": "Greet", "session": "sess-cli"}})
	// kern_mask_pii is a pure ungoverned tool: the extra agent_id arg flows
	// through to the stats recorder without triggering authorization.
	args2, _ := json.Marshal(map[string]any{"name": "kern_mask_pii", "arguments": map[string]any{"text": "the token is sk-abcdefghijklmnopqrstuvwxyz1234567890", "session": "sess-agent", "agent_id": "fixer-7"}})
	resps := serveMany(t, init,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":`+string(args1)+`}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":`+string(args2)+`}`)
	for i, r := range resps {
		if isErrorResult(r) {
			t.Fatalf("response %d errored: %q", i, contentText(r))
		}
	}

	rec, err := stats.NewRecorder()
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	es, err := rec.Entries(100)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var cli, agent *stats.Entry
	for i := range es {
		switch es[i].Session {
		case "sess-cli":
			cli = &es[i]
		case "sess-agent":
			agent = &es[i]
		}
	}
	if cli == nil {
		t.Fatalf("no entry for sess-cli (%d entries)", len(es))
	}
	if cli.Agent != "opencode" {
		t.Errorf("session without agent_id must record the initialize client name, got %q", cli.Agent)
	}
	if agent == nil {
		t.Fatalf("no entry for sess-agent (%d entries)", len(es))
	}
	if agent.Agent != "fixer-7" {
		t.Errorf("explicit agent_id must win over the client name, got %q", agent.Agent)
	}
}
