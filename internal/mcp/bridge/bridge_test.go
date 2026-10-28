package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcpclient"
)

// echoServerPath is the shared stdio MCP fixture used by internal/mcpclient's
// own tests; the bridge is exercised against the same server so the transport
// behavior under test is real, not mocked.
func echoServerPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "mcpclient", "testdata", "echo_server", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("echo MCP server fixture missing: %v", err)
	}
	return p
}

// TestCallToolRequiresArguments pins the argument contract before any I/O.
func TestCallToolRequiresArguments(t *testing.T) {
	ctx := context.Background()
	if _, err := CallTool(ctx, map[string]any{"tool": "x"}); err == nil || !strings.Contains(err.Error(), "server is required") {
		t.Fatalf("missing server: %v", err)
	}
	if _, err := CallTool(ctx, map[string]any{"server": "s"}); err == nil || !strings.Contains(err.Error(), "tool is required") {
		t.Fatalf("missing tool: %v", err)
	}
}

// TestCallToolUnknownServer pins the actionable error for an unconfigured
// server name (the agent's most likely mistake).
func TestCallToolUnknownServer(t *testing.T) {
	root := t.TempDir()
	_, err := CallTool(context.Background(), map[string]any{
		"root": root, "server": "nope", "tool": "echo",
	})
	if err == nil || !strings.Contains(err.Error(), "no configured MCP server") {
		t.Fatalf("expected unknown-server error, got %v", err)
	}
}

// TestCallToolRejectsCorruptConfig pins that a malformed config is surfaced
// rather than silently treated as "no servers".
func TestCallToolRejectsCorruptConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp-servers.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CallTool(context.Background(), map[string]any{"root": root, "server": "s", "tool": "t"}); err == nil {
		t.Fatal("corrupt config must error")
	}
}

// TestCallToolForwardsThroughEchoServer pins the success path end to end:
// config → dial → call → JSON envelope, including the mcp__server__tool prefix
// strip that lets an agent pass a namespaced tool name through unchanged.
func TestCallToolForwardsThroughEchoServer(t *testing.T) {
	root := t.TempDir()
	server := mcpclient.Server{
		Name:      "echo",
		Transport: "stdio",
		Command:   "go",
		Args:      []string{"run", echoServerPath(t)},
	}
	if err := mcpclient.SaveConfig(root, []mcpclient.Server{server}); err != nil {
		t.Fatal(err)
	}

	for _, tool := range []string{"echo", "mcp__echo__echo"} {
		out, err := CallTool(context.Background(), map[string]any{
			"root":      root,
			"server":    "echo",
			"tool":      tool,
			"arguments": map[string]any{"text": "hello"},
		})
		if err != nil {
			t.Fatalf("tool %q: %v", tool, err)
		}
		var env struct {
			Server string         `json:"server"`
			Tool   string         `json:"tool"`
			Public string         `json:"public"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("envelope not JSON: %v\n%s", err, out)
		}
		if env.Server != "echo" || env.Tool != "echo" {
			t.Fatalf("tool %q: unexpected envelope %+v", tool, env)
		}
		if env.Public != "mcp__echo__echo" {
			t.Fatalf("public name not restored: %q", env.Public)
		}
		if env.Result["reply"] != "hello" {
			t.Fatalf("result not forwarded: %+v", env.Result)
		}
	}
}

// TestCallToolDialFailurePropagates pins that a broken server surfaces as an
// error instead of an empty success envelope.
func TestCallToolDialFailurePropagates(t *testing.T) {
	root := t.TempDir()
	server := mcpclient.Server{
		Name:      "broken",
		Transport: "stdio",
		Command:   filepath.Join(root, "definitely-not-a-binary"),
	}
	if err := mcpclient.SaveConfig(root, []mcpclient.Server{server}); err != nil {
		t.Fatal(err)
	}
	if _, err := CallTool(context.Background(), map[string]any{"root": root, "server": "broken", "tool": "echo"}); err == nil {
		t.Fatal("dial failure must propagate")
	}
}
