package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

// samplingHarness wires a Server to an in-process mock MCP client over real
// pipes, driving Serve() like a stdio host would.
type samplingHarness struct {
	s        *Server
	clientW  *io.PipeWriter // client -> server
	clientR  *io.PipeReader // server -> client
	serveErr chan error
}

func newSamplingHarness(t *testing.T) *samplingHarness {
	t.Helper()
	srvIn, clientW := io.Pipe()  // server reads; client writes
	clientR, srvOut := io.Pipe() // client reads; server writes
	s := NewServer(srvIn, srvOut)
	h := &samplingHarness{
		s:        s,
		clientW:  clientW,
		clientR:  clientR,
		serveErr: make(chan error, 1),
	}
	go func() { h.serveErr <- s.Serve() }()
	t.Cleanup(func() {
		s.Close() // disposes the host sampler if registered
		_ = clientW.CloseWithError(io.EOF)
		_ = srvOut.Close()
		select {
		case <-h.serveErr:
		case <-time.After(2 * time.Second):
		}
	})
	return h
}

// write sends a JSON line to the server (as the client).
func (h *samplingHarness) write(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := h.clientW.Write(append(data, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// sendInitialize announces the given capabilities and returns once the
// server has processed the initialize round-trip (its response is read and
// discarded here synchronously).
func (h *samplingHarness) sendInitialize(t *testing.T, caps map[string]any) {
	t.Helper()
	h.write(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    caps,
		},
	})
	// Read the server's initialize response.
	line, err := readLine(h.clientR)
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	var resp struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil || resp.ID != 1 {
		t.Fatalf("unexpected initialize response: %s (err %v)", line, err)
	}
}

func readLine(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		return "", sc.Err()
	}
	return sc.Text(), nil
}

// waitSampler polls llm.HasHostSampler until it reaches want (registration
// happens inside the Serve loop and is async relative to the test).
func waitSampler(t *testing.T, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if llm.HasHostSampler() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("HasHostSampler never reached %v", want)
}

// TestHostSamplingRoundTrip: a stdio client announcing sampling capability
// gets a host sampler registered; an LLM Generate through the MCP provider
// performs a sampling/createMessage round-trip and returns the host's reply.
func TestHostSamplingRoundTrip(t *testing.T) {
	h := newSamplingHarness(t)
	h.sendInitialize(t, map[string]any{"sampling": map[string]any{}})
	waitSampler(t, true)

	// Mock host: read the sampling request and reply.
	go func() {
		line, err := readLine(h.clientR)
		if err != nil {
			return
		}
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.Method != "sampling/createMessage" {
			return
		}
		var params struct {
			SystemPrompt string `json:"systemPrompt"`
			Messages     []struct {
				Content map[string]any `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(req.Params, &params)
		text, _ := params.Messages[0].Content["text"].(string)
		h.write(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"role":    "assistant",
				"content": map[string]any{"type": "text", "text": "host reply to: " + text},
			},
		})
	}()

	p := llm.NewMCPProvider()
	out, err := p.Generate(context.Background(), "sys", "user prompt", llm.Options{})
	if err != nil {
		t.Fatalf("Generate via host sampler: %v", err)
	}
	if !strings.Contains(out, "host reply to: user prompt") {
		t.Errorf("unexpected host reply: %q", out)
	}
}

// TestHostSamplingNotRegisteredWithoutCapability: a client that does not
// announce sampling gets no host sampler (the auto chain stays local-only).
func TestHostSamplingNotRegisteredWithoutCapability(t *testing.T) {
	h := newSamplingHarness(t)
	h.sendInitialize(t, map[string]any{})
	// Give the serve loop a moment to (not) register.
	time.Sleep(150 * time.Millisecond)
	if llm.HasHostSampler() {
		t.Fatal("host sampler registered despite missing sampling capability")
	}
}

// TestHostSamplingTimeout: a host that never replies fails the round-trip
// with a clear error so the LLM chain moves on to the next provider.
func TestHostSamplingTimeout(t *testing.T) {
	h := newSamplingHarness(t)
	h.sendInitialize(t, map[string]any{"sampling": map[string]any{}})
	waitSampler(t, true)

	// Mock host: read the sampling request but never reply.
	go func() { _, _ = readLine(h.clientR) }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p := llm.NewMCPProvider()
	_, err := p.Generate(ctx, "", "user", llm.Options{})
	if err == nil {
		t.Fatal("expected an error from a silent host")
	}
	if !strings.Contains(err.Error(), "cancelled") && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected a cancelled/timed-out error, got: %v", err)
	}
}

// TestAutoChainHostFirstWhenSamplerRegistered: with a host sampler active,
// the auto chain puts the host provider FIRST (host agent delegation),
// before ollama and local agent CLIs.
func TestAutoChainHostFirstWhenSamplerRegistered(t *testing.T) {
	h := newSamplingHarness(t)
	h.sendInitialize(t, map[string]any{"sampling": map[string]any{}})
	waitSampler(t, true)
	defer func() {
		h.s.Close()
		waitSampler(t, false)
	}()

	p, err := llm.NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	chain, ok := p.(*llm.ChainProvider)
	if !ok {
		t.Fatalf("auto provider = %T, want *llm.ChainProvider", p)
	}
	provs := chain.Providers()
	if len(provs) == 0 {
		t.Fatal("empty chain")
	}
	if _, ok := provs[0].(*llm.MCPProvider); !ok {
		t.Errorf("chain[0] = %T, want *llm.MCPProvider (host first)", provs[0])
	}
	if _, ok := provs[1].(*llm.OllamaProvider); !ok {
		t.Errorf("chain[1] = %T, want *llm.OllamaProvider", provs[1])
	}
}

// TestRegisterCommandSamplerTool: kern_register_host_sampler registers a
// command sampler (stdin = user prompt, $KERN_SYSTEM_PROMPT = system prompt,
// stdout = reply); empty command unregisters. Exercises the handler directly
// (no serve loop needed) and generation through the global MCP provider.
func TestRegisterCommandSamplerTool(t *testing.T) {
	s := newTestServer()
	ctx := context.Background()
	p := llm.NewMCPProvider()
	t.Cleanup(func() { _, _ = s.handleRegisterHostSampler(ctx, map[string]any{"command": ""}) })

	// Register: `cat` echoes the user prompt from stdin.
	out, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": "cat", "timeout": "30"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(out, "host sampler registered") {
		t.Errorf("registration message = %q", out)
	}
	res, err := p.Generate(ctx, "SYS", "hello via stdin", llm.Options{})
	if err != nil {
		t.Fatalf("Generate(cat): %v", err)
	}
	if res != "hello via stdin" {
		t.Errorf("Generate(cat) = %q, want the stdin prompt echoed", res)
	}

	// System prompt flows via $KERN_SYSTEM_PROMPT.
	if _, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": `printf 'sys=%s' "$KERN_SYSTEM_PROMPT"`}); err != nil {
		t.Fatalf("register sys: %v", err)
	}
	res, err = p.Generate(ctx, "MY-SYS", "u", llm.Options{})
	if err != nil {
		t.Fatalf("Generate(sys): %v", err)
	}
	if res != "sys=MY-SYS" {
		t.Errorf("Generate(sys) = %q, want sys=MY-SYS", res)
	}

	// A failing command surfaces its stderr.
	if _, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": "echo boom >&2; exit 3"}); err != nil {
		t.Fatalf("register fail: %v", err)
	}
	if _, err := p.Generate(ctx, "", "u", llm.Options{}); err == nil || !strings.Contains(err.Error(), "host sampler command failed") {
		t.Errorf("expected a command-failure error, got %v", err)
	}

	// Unregister: the host leg is gone.
	msg, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": ""})
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if !strings.Contains(msg, "unregistered") {
		t.Errorf("unregister message = %q", msg)
	}
	if _, err := p.Generate(ctx, "", "u", llm.Options{}); err == nil {
		t.Error("Generate must fail after unregister")
	}
}

// TestRegisterCommandSamplerMultiKey: registrations under different keys
// coexist (multi-agent/repo), and unregistering one key leaves the other.
func TestRegisterCommandSamplerMultiKey(t *testing.T) {
	s := newTestServer()
	ctx := context.Background()
	p := llm.NewMCPProvider()
	t.Cleanup(func() {
		_, _ = s.handleRegisterHostSampler(ctx, map[string]any{"command": "", "key": "repo-a"})
		_, _ = s.handleRegisterHostSampler(ctx, map[string]any{"command": "", "key": "repo-b"})
	})

	if _, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": "printf 'A:%s' \"$KERN_SYSTEM_PROMPT\"", "key": "repo-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": "printf 'B:%s' \"$KERN_SYSTEM_PROMPT\"", "key": "repo-b"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.Generate(ctx, "X", "u", llm.Options{})
	if err != nil {
		t.Fatalf("Generate(multi): %v", err)
	}
	if res != "A:X" {
		t.Errorf("got %q, want A:X (first registered key wins)", res)
	}

	// Unregister repo-a only: repo-b answers.
	if _, err := s.handleRegisterHostSampler(ctx, map[string]any{"command": "", "key": "repo-a"}); err != nil {
		t.Fatal(err)
	}
	res, err = p.Generate(ctx, "X", "u", llm.Options{})
	if err != nil {
		t.Fatalf("Generate after unregistering repo-a: %v", err)
	}
	if res != "B:X" {
		t.Errorf("got %q, want B:X", res)
	}
}

// TestServerSelfRegistersCommandSamplerFromEnv: KERN_HOST_SAMPLER_CMD makes
// the server self-register a command sampler at startup — the host leg for
// hosts that do not announce MCP sampling (e.g. opencode). The auto chain
// then delegates generation to the command.
func TestServerSelfRegistersCommandSamplerFromEnv(t *testing.T) {
	t.Setenv("KERN_HOST_SAMPLER_CMD", "cat")
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	if !llm.HasHostSampler() {
		t.Fatal("KERN_HOST_SAMPLER_CMD must self-register a host sampler at startup")
	}
	p := llm.NewMCPProvider()
	out, err := p.Generate(context.Background(), "", "env-registered", llm.Options{})
	if err != nil {
		t.Fatalf("Generate via env-registered sampler: %v", err)
	}
	if out != "env-registered" {
		t.Errorf("got %q, want the stdin prompt echoed", out)
	}
}
