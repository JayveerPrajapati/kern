package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var errDenied = errors.New("blocked by test gate")

// recorder is a test tool that records whether it executed, so tests can prove
// the pre-tool hook runs BEFORE any side effect.
type recorder struct {
	called bool
}

func (r *recorder) Name() string        { return "test_recorder" }
func (r *recorder) Description() string { return "records execution" }
func (r *recorder) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (r *recorder) Handle(ctx context.Context, args json.RawMessage) ToolResult {
	r.called = true
	return NewTextResult("ran")
}

// serveOne feeds a single JSON-RPC request into the server's Serve loop and
// returns the first response line.
func serveOne(t *testing.T, s *Server, request string) string {
	t.Helper()
	var sb strings.Builder
	ctx := context.Background()
	if err := s.Serve(ctx, strings.NewReader(request), &sb); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(sb.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("no response from server")
	}
	return lines[0]
}

// TestPreToolHook_NilHookIsNoop mirrors kern's nil-hook case: with no hook
// registered, the tool executes normally.
func TestPreToolHook_NilHookIsNoop(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &recorder{}
	s.RegisterTool(r)

	resp := serveOne(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test_recorder","arguments":{}}}`)

	if !r.called {
		t.Fatal("nil hook must not prevent tool execution")
	}
	if !strings.Contains(resp, `"ran"`) {
		t.Fatalf("expected tool result in response, got: %s", resp)
	}
}

// TestPreToolHook_DenyBlocksBeforeExecution mirrors kern's deny case: a hook
// returning an error blocks the call before the handler runs, surfaced as a
// tool error (isError=true).
func TestPreToolHook_DenyBlocksBeforeExecution(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &recorder{}
	s.RegisterTool(r)

	s.WithPreToolHook(func(name string, args map[string]any) error {
		return errDenied
	})

	resp := serveOne(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test_recorder","arguments":{}}}`)

	if r.called {
		t.Fatal("denied tool must not execute")
	}
	if !strings.Contains(resp, `"isError":true`) {
		t.Fatalf("denial must surface as isError=true, got: %s", resp)
	}
	if !strings.Contains(resp, "pre-tool-use denied") {
		t.Fatalf("denial must explain why, got: %s", resp)
	}
}

// TestPreToolHook_AllowRunsNormally mirrors kern's allow case: a hook that
// returns nil lets the call proceed.
func TestPreToolHook_AllowRunsNormally(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &recorder{}
	s.RegisterTool(r)

	s.WithPreToolHook(func(name string, args map[string]any) error {
		if name != "test_recorder" {
			t.Fatalf("hook received wrong tool name: %s", name)
		}
		return nil
	})

	resp := serveOne(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test_recorder","arguments":{}}}`)

	if !r.called {
		t.Fatal("allowed tool must execute")
	}
	if strings.Contains(resp, `"isError":true`) {
		t.Fatalf("allowed tool must not be an error, got: %s", resp)
	}
}

// TestPreToolHook_GateSeesToolNameAndArgs verifies the hook receives the tool
// name and parsed arguments (the same contract as kern's hook).
func TestPreToolHook_GateSeesToolNameAndArgs(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &recorder{}
	s.RegisterTool(r)

	var gotName string
	var gotRepo string
	s.WithPreToolHook(func(name string, args map[string]any) error {
		gotName = name
		gotRepo, _ = args["repo"].(string)
		return nil
	})

	serveOne(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test_recorder","arguments":{"repo":"/tmp/example"}}}`)

	if gotName != "test_recorder" {
		t.Fatalf("hook got name %q, want test_recorder", gotName)
	}
	if gotRepo != "/tmp/example" {
		t.Fatalf("hook got repo %q, want /tmp/example", gotRepo)
	}
}
