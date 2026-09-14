// Package mcpclient is a minimal stdlib-only MCP (Model Context Protocol)
// client: it connects to external MCP servers over stdio or
// streamable-http, lists their tools, and calls them. Tools only — MCP
// resources and prompts are unsupported (mirroring dsh-mcp-client's scope).
// Public tool names follow the dsh naming contract: mcp__<server>__<tool>,
// normalized to [A-Za-z0-9_-] with a SHA-256 suffix on lossy normalization,
// raw name only ever sent on the wire.
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ProtocolVersion is the MCP protocol version the client speaks.
const ProtocolVersion = "2025-03-26"

// DefaultConfigPath is the repo-relative config file (JSON array of Server).
const DefaultConfigPath = ".kern/mcp-servers.json"

// Server is one configured external MCP server.
type Server struct {
	// Name is the tool-name namespace: [A-Za-z0-9_-]{1,32}, unique per root.
	Name string `json:"name"`
	// Transport selects the connection: "stdio" or "streamable-http".
	Transport string `json:"transport"`
	// stdio fields: executable, arguments, extra env, working directory.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	// streamable-http fields: endpoint URL and extra request headers.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Tool is one tool a server exposes.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Validate checks a server config for transport-consistent fields.
func (s *Server) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if len(s.Name) > 32 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(s.Name) {
		return fmt.Errorf("server name must match [A-Za-z0-9_-]{1,32}")
	}
	switch s.Transport {
	case "stdio":
		if s.Command == "" {
			return fmt.Errorf("stdio transport requires a command")
		}
	case "streamable-http":
		if s.URL == "" {
			return fmt.Errorf("streamable-http transport requires a url")
		}
	case "unix", "uds":
		if s.URL == "" {
			return fmt.Errorf("unix transport requires a socket path in url (e.g. /tmp/kern.sock or unix:///tmp/kern.sock)")
		}
	default:
		return fmt.Errorf("unknown transport %q (use stdio, streamable-http, or unix)", s.Transport)
	}
	return nil
}

// nameChars is the allowed public-name charset per the DeepSeek function-name
// contract; anything else is dropped during normalization.
var nameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// maxPublicName is the DeepSeek function-name length contract.
const maxPublicName = 64

// hashSuffixLength is the hex SHA-256 length appended on lossy normalization.
const hashSuffixLength = 12

// PublicName returns the model-facing bridged name for a server tool:
// mcp__<server>__<tool>, normalized to [A-Za-z0-9_-] and capped at 64 chars
// (a SHA-256 suffix is appended when normalization loses information or the
// name would exceed the cap). The raw tool name is only ever sent on the wire.
func PublicName(server, tool string) string {
	raw := "mcp__" + server + "__" + tool
	normalized := nameChars.ReplaceAllString(raw, "_")
	if normalized == raw && len(normalized) <= maxPublicName {
		return normalized
	}
	h := sha256.Sum256([]byte(raw))
	suffix := hex.EncodeToString(h[:])[:hashSuffixLength]
	// Keep the readable prefix under the cap, then append the hash suffix.
	prefix := normalized
	if len(prefix) > maxPublicName-hashSuffixLength-1 {
		prefix = prefix[:maxPublicName-hashSuffixLength-1]
	}
	return prefix + "-" + suffix
}

// ---- JSON-RPC framing ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---- stdio transport ----

type stdioClient struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner
}

// startStdio spawns the server process and performs the initialize handshake.
func startStdio(ctx context.Context, s *Server) (*stdioClient, error) {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = scrubEnv(os.Environ(), s.Env)
	if s.Cwd != "" {
		cmd.Dir = s.Cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %v", s.Command, err)
	}
	c := &stdioClient{cmd: cmd, in: stdin, out: bufio.NewScanner(stdout)}
	c.out.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if err := c.initialize(s); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// scrubEnv merges extra env vars over the ambient environment (MCP servers
// should not see host secrets beyond what the config explicitly adds).
func scrubEnv(base []string, extra map[string]string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

// initialize performs the MCP initialize handshake on the stdio transport.
func (c *stdioClient) initialize(s *Server) error {
	req := rpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kern", "version": "1"},
	}}
	if _, err := c.roundTrip(req); err != nil {
		return err
	}
	// notifications/initialized is fire-and-forget.
	_ = c.send(rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	return nil
}

func (c *stdioClient) send(req rpcRequest) error {
	enc, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.in.Write(append(enc, '\n')); err != nil {
		return err
	}
	return nil
}

// roundTrip sends one request and reads lines until the matching response.
func (c *stdioClient) roundTrip(req rpcRequest) (any, error) {
	if err := c.send(req); err != nil {
		return nil, err
	}
	for c.out.Scan() {
		line := bytes.TrimSpace(c.out.Bytes())
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // ignore server notifications / malformed lines
		}
		if resp.ID != req.ID {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
	return nil, fmt.Errorf("server closed stdout: %v", c.out.Err())
}

// Close terminates the server process.
func (c *stdioClient) Close() {
	_ = c.in.Close()
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
}

// ---- streamable-http transport ----

type httpClient struct {
	server  *Server
	client  *http.Client
	headers map[string]string
}

func startHTTP(s *Server) *httpClient {
	return &httpClient{
		server: s,
		client: &http.Client{Timeout: 30 * time.Second},
		headers: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json, text/event-stream",
		},
	}
}

func startUnix(s *Server) *httpClient {
	sockPath := s.URL
	if strings.HasPrefix(sockPath, "unix://") {
		sockPath = strings.TrimPrefix(sockPath, "unix://")
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	return &httpClient{
		server: s,
		client: &http.Client{
			Transport: tr,
			Timeout:   60 * time.Second,
		},
		headers: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json, text/event-stream",
		},
	}
}

// roundTrip performs one JSON-RPC request as a single POST (stateless; no
// session negotiation needed for tools/list and tools/call).
func (c *httpClient) roundTrip(req rpcRequest) (any, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	targetURL := c.server.URL
	if c.server.Transport == "unix" || c.server.Transport == "uds" {
		targetURL = "http://localhost/mcp"
	}
	hreq, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		hreq.Header.Set(k, v)
	}
	for k, v := range c.server.Headers {
		hreq.Header.Set(k, v)
	}
	resp, err := c.client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var r rpcResponse
	if err := json.Unmarshal(data, &r); err != nil {
		// streamable-http may return SSE frames; a single JSON body is the
		// common stateless case — surface the raw body on parse failure.
		return nil, fmt.Errorf("unparseable MCP response: %s", strings.TrimSpace(string(data)))
	}
	if r.Error != nil {
		return nil, fmt.Errorf("server error %d: %s", r.Error.Code, r.Error.Message)
	}
	return r.Result, nil
}

// ---- Client facade ----

// Client is an MCP client bound to one server config.
type Client struct {
	server *Server
	stdio  *stdioClient
	http   *httpClient
}

// Dial connects to a server. Use Close to release the process.
func Dial(ctx context.Context, s *Server) (*Client, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	c := &Client{server: s}
	switch s.Transport {
	case "stdio":
		sc, err := startStdio(ctx, s)
		if err != nil {
			return nil, err
		}
		c.stdio = sc
	case "streamable-http":
		c.http = startHTTP(s)
	case "unix", "uds":
		c.http = startUnix(s)
	}
	return c, nil
}

// ListTools returns the server's model-visible tool names and descriptions.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := c.roundTrip(rpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if err != nil {
		return nil, err
	}
	raw, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected tools/list result shape")
	}
	items, _ := raw["tools"].([]any)
	out := make([]Tool, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		t := Tool{Name: name}
		if d, ok := m["description"].(string); ok {
			t.Description = d
		}
		if sch, ok := m["inputSchema"].(map[string]any); ok {
			t.InputSchema = sch
		}
		out = append(out, t)
	}
	return out, nil
}

// CallTool invokes a tool by its raw wire name with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	res, err := c.roundTrip(rpcRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: params})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) roundTrip(req rpcRequest) (any, error) {
	if c.stdio != nil {
		return c.stdio.roundTrip(req)
	}
	return c.http.roundTrip(req)
}

// Close releases the stdio process (no-op for streamable-http).
func (c *Client) Close() {
	if c.stdio != nil {
		c.stdio.Close()
	}
}

// ---- Config persistence ----

// LoadConfig reads the server config from root/.kern/mcp-servers.json.
// A missing file returns an empty (valid) config.
func LoadConfig(root string) ([]Server, error) {
	path := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var servers []Server
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	return servers, nil
}

// SaveConfig writes the server config to root/.kern/mcp-servers.json.
func SaveConfig(root string, servers []Server) error {
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// FindServer returns the configured server with the given name.
func FindServer(servers []Server, name string) (Server, bool) {
	for _, s := range servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}
