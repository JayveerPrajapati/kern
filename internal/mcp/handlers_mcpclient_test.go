package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcpclient"
)

// configureEchoServer registers the testdata echo server into the root's
// .kern/mcp-servers.json so kern_mcp_call can reach it.
func configureEchoServer(t *testing.T, root string) {
	t.Helper()
	servers := []mcpclient.Server{{
		Name:      "echo",
		Transport: "stdio",
		Command:   "go",
		Args:      []string{"run", "../mcpclient/testdata/echo_server/main.go"},
	}}
	if err := mcpclient.SaveConfig(root, servers); err != nil {
		t.Fatal(err)
	}
}

func TestMcpCallBridgesEchoTool(t *testing.T) {
	root := t.TempDir()
	configureEchoServer(t, root)

	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{root}

	res, err := s.handleMcpCall(context.Background(), map[string]any{
		"server":    "echo",
		"tool":      "echo",
		"arguments": map[string]any{"text": "hello from kern"},
		"root":      root,
	})
	if err != nil {
		t.Fatalf("handleMcpCall error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("result should be JSON: %v", err)
	}
	if parsed["server"] != "echo" {
		t.Errorf("unexpected server: %v", parsed["server"])
	}
	result, _ := parsed["result"].(map[string]any)
	if result["reply"] != "hello from kern" {
		t.Errorf("expected echoed reply, got %v", result)
	}
}

func TestMcpCallPublicNameAccepted(t *testing.T) {
	root := t.TempDir()
	configureEchoServer(t, root)

	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{root}

	res, err := s.handleMcpCall(context.Background(), map[string]any{
		"server":    "echo",
		"tool":      "mcp__echo__echo",
		"arguments": map[string]any{"text": "hi"},
		"root":      root,
	})
	if err != nil {
		t.Fatalf("handleMcpCall with public name error: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal([]byte(res), &parsed)
	if parsed["public"] != "mcp__echo__echo" {
		t.Errorf("public name mismatch: %v", parsed["public"])
	}
}

func TestMcpCallUnknownServer(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{t.TempDir()}
	_, err := s.handleMcpCall(context.Background(), map[string]any{
		"server": "nope",
		"tool":   "x",
		"root":   s.roots[0],
	})
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "no configured MCP server") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestMcpCallMissingFields(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	if _, err := s.handleMcpCall(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error when server missing")
	}
}
