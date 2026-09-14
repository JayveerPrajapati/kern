package mcpclient

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicNameContract(t *testing.T) {
	cases := []struct{ server, tool, want string }{
		{"github", "create_issue", "mcp__github__create_issue"},
		{"web", "web_search", "mcp__web__web_search"},
		{"a.b", "x", "mcp__a_b__x"}, // lossy: dot replaced, suffix appended
	}
	for _, c := range cases {
		got := PublicName(c.server, c.tool)
		if !strings.HasPrefix(got, "mcp__") {
			t.Errorf("PublicName(%q,%q) missing mcp__ prefix: %q", c.server, c.tool, got)
		}
		for _, r := range got {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", r) {
				t.Errorf("PublicName(%q,%q) has invalid char %q", c.server, c.tool, r)
			}
		}
		if len(got) > 64 {
			t.Errorf("PublicName(%q,%q) exceeds 64 chars: %d", c.server, c.tool, len(got))
		}
	}
	if PublicName("github", "create_issue") != "mcp__github__create_issue" {
		t.Error("lossless names must be unchanged")
	}
	// Lossy normalization must be deterministic and carry the hash suffix.
	a, b := PublicName("a.b", "x"), PublicName("a.b", "x")
	if a != b || !strings.Contains(a, "-") {
		t.Errorf("lossy name must be deterministic with hash suffix: %q", a)
	}
}

func TestServerValidate(t *testing.T) {
	good := []Server{
		{Name: "gh", Transport: "stdio", Command: "npx"},
		{Name: "web", Transport: "streamable-http", URL: "http://localhost:3000/mcp"},
		{Name: "uds", Transport: "unix", URL: "/tmp/kern.sock"},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("valid server flagged: %v", err)
		}
	}
	bad := []Server{
		{Transport: "stdio", Command: "x"},                               // no name
		{Name: "too long name here x", Transport: "stdio", Command: "x"}, // name > 32 / invalid chars
		{Name: "a", Transport: "stdio"},                                  // no command
		{Name: "a", Transport: "streamable-http"},                        // no url
		{Name: "a", Transport: "unix"},                                   // no url/sock
		{Name: "a", Transport: "bogus", Command: "x"},                    // bad transport
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("invalid server accepted: %+v", s)
		}
	}
}

func TestConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	servers := []Server{
		{Name: "gh", Transport: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
		{Name: "web", Transport: "streamable-http", URL: "http://localhost:3000/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
	}
	if err := SaveConfig(root, servers); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "gh" || got[1].URL != "http://localhost:3000/mcp" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if _, ok := FindServer(got, "gh"); !ok {
		t.Error("FindServer should locate gh")
	}
	if _, ok := FindServer(got, "nope"); ok {
		t.Error("FindServer should miss unknown")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	root := t.TempDir()
	got, err := LoadConfig(root)
	if err != nil || got != nil {
		t.Fatalf("missing config should be empty, got %v err %v", got, err)
	}
}

func TestConfigWrittenToGitignoredKernDir(t *testing.T) {
	root := t.TempDir()
	if err := SaveConfig(root, []Server{{Name: "x", Transport: "stdio", Command: "echo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "mcp-servers.json")); err != nil {
		t.Errorf("config should live under .kern: %v", err)
	}
}

// TestDialStdioEchoServer verifies the full stdio round-trip against a tiny
// in-process JSON-RPC server speaking the MCP protocol over stdin/stdout.
func TestDialStdioEchoServer(t *testing.T) {
	server := Server{
		Name:      "echo",
		Transport: "stdio",
		Command:   "go",
		Args:      []string{"run", "testdata/echo_server/main.go"},
	}
	ctx := context.Background()
	c, err := Dial(ctx, &server)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["reply"] != "hello" {
		t.Errorf("expected echoed reply, got %v", res)
	}
}

func TestDialUnixEchoServer(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix sockets not supported or failed: %v", err)
	}
	defer func() { _ = ln.Close() }()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "tools/list" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "echo_uds", "description": "echo over UDS"},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"echoed": true,
			},
		})
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	server := Server{
		Name:      "echo_uds",
		Transport: "unix",
		URL:       sockPath,
	}
	ctx := context.Background()
	c, err := Dial(ctx, &server)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo_uds" {
		t.Fatalf("unexpected tools over unix socket: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo_uds", map[string]any{})
	if err != nil {
		t.Fatalf("call tool over unix: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["echoed"] != true {
		t.Errorf("expected echoed reply over UDS, got %v", res)
	}
}
