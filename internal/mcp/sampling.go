package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

// samplingTimeout caps a single host sampling round-trip. The LLM chain
// treats a timeout as a provider failure and moves on to the next provider
// (ollama, then local agent CLIs).
const samplingTimeout = 120 * time.Second

// cmdSamplerTimeout is the default per-call timeout for explicitly
// registered command samplers (KERN_HOST_SAMPLER_TIMEOUT seconds, default 180).
func cmdSamplerTimeout() time.Duration {
	if v := os.Getenv("KERN_HOST_SAMPLER_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 180 * time.Second
}

// hostSamplerSeq numbers Server instances so each connection registers its
// samplers under a unique key (multi-client / multi-agent coexistence).
var hostSamplerSeq atomic.Int64

// samplerKeyFor returns a unique sampler key for this Server instance.
func samplerKeyFor() string {
	return fmt.Sprintf("mcp-conn-%d", hostSamplerSeq.Add(1))
}

// samplingReply is the correlated outcome of a server-initiated
// sampling/createMessage request to the host client.
type samplingReply struct {
	result map[string]any
	err    error
}

// registerHostSampling wires the connected MCP host into the LLM provider
// chain (llm.RegisterHostSamplerFor, under this server's key) so LLM-dependent
// features (heal, optimize, planner) delegate generation to the host agent via
// sampling when no local LLM is available. Only the stdio transport can
// correlate the response, so Streamable HTTP clients are skipped. The returned
// disposer unregisters the sampler (Close calls it) — registrations are effects.
func (s *Server) registerHostSampling() func() {
	if s.transport != "stdio" {
		// Streamable HTTP cannot push or correlate responses: host sampling
		// is stdio-only by design.
		return func() {}
	}
	disp := llm.RegisterHostSamplerFor(s.samplerKey, s.sample)
	s.samplingMu.Lock()
	s.samplingSlots[s.samplerKey] = disp
	s.samplingHost = true
	s.samplingMu.Unlock()
	return disp
}

// registerCommandSampler registers (or, with an empty command, unregisters)
// the explicit host-sampler command for this server's slot. The command runs
// via sh -c for every generation: the user prompt on stdin, the system prompt
// in $KERN_SYSTEM_PROMPT, stdout is the reply. Unregistering restores the
// passive MCP-sampling sampler when the client announced sampling capability.
// key allows advanced callers to namespace a registration beyond the server's
// own slot (e.g. per agent or per repo); the empty key uses this server's slot.
func (s *Server) registerCommandSampler(cmd string, timeout time.Duration, key string) (string, error) {
	if key == "" {
		key = s.samplerKey
	}
	if strings.TrimSpace(cmd) == "" {
		s.samplingMu.Lock()
		if d, ok := s.samplingSlots[key]; ok {
			d()
			delete(s.samplingSlots, key)
		}
		if key == s.samplerKey {
			s.samplingHost = false
			// Restore passive MCP sampling when the client announced it.
			if s.samplingCapable && s.transport == "stdio" {
				s.samplingSlots[key] = llm.RegisterHostSamplerFor(key, s.sample)
				s.samplingHost = true
			}
		}
		s.samplingMu.Unlock()
		return fmt.Sprintf("host sampler unregistered (key %s)", key), nil
	}
	if timeout <= 0 {
		timeout = cmdSamplerTimeout()
	}
	sampler := func(ctx context.Context, system, user string, opts llm.Options) (string, error) {
		return runHostSamplerCommand(ctx, cmd, timeout, system, user)
	}
	disp := llm.RegisterHostSamplerFor(key, sampler)
	s.samplingMu.Lock()
	if d, ok := s.samplingSlots[key]; ok {
		d() // replace only THIS key; other keys' samplers are untouched
	}
	s.samplingSlots[key] = disp
	if key == s.samplerKey {
		s.samplingHost = true
	}
	s.samplingMu.Unlock()
	return fmt.Sprintf("host sampler registered (key %s): %s", key, cmd), nil
}

// handleRegisterHostSampler implements kern_register_host_sampler.
func (s *Server) handleRegisterHostSampler(ctx context.Context, args map[string]any) (string, error) {
	cmd := argString(args, "command")
	key := argString(args, "key")
	timeout := time.Duration(0)
	if v := argString(args, "timeout"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs <= 0 {
			return "", fmt.Errorf("timeout: invalid seconds %q", v)
		}
		timeout = time.Duration(secs) * time.Second
	}
	model := argString(args, "model")
	msg, err := s.registerCommandSampler(cmd, timeout, key)
	if err != nil {
		return "", err
	}
	if model != "" {
		msg += " (model " + model + ")"
	}
	return msg, nil
}

// runHostSamplerCommand executes a registered sampler command via sh -c.
// The user prompt is fed on stdin, the system prompt exported as
// $KERN_SYSTEM_PROMPT; stdout (trimmed) is the reply. Output is capped at
// 64KiB so a misbehaving command cannot flood the caller.
func runHostSamplerCommand(ctx context.Context, cmd string, timeout time.Duration, system, user string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(cctx, "sh", "-c", cmd)
	c.Stdin = strings.NewReader(user)
	c.Env = append(os.Environ(), "KERN_SYSTEM_PROMPT="+system)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return "", fmt.Errorf("llm: host sampler command failed: %s", msg)
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("llm: host sampler command produced no output")
	}
	if out.Len() > 64<<10 {
		return "", fmt.Errorf("llm: host sampler output exceeds the 64KiB cap")
	}
	return strings.TrimSpace(out.String()), nil
}

// sample implements llm.Sampler: it sends a sampling/createMessage request
// to the connected MCP host and waits for the correlated response. It is the
// last-resort leg of the auto LLM chain (host first when registered, per the
// host-agent-delegation design; the chain falls through on any failure).
func (s *Server) sample(ctx context.Context, system, user string, opts llm.Options) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.samplingMu.Lock()
	if !s.samplingHost {
		s.samplingMu.Unlock()
		return "", fmt.Errorf("mcp: host sampling not active")
	}
	s.samplingSeq++
	id := fmt.Sprintf("%d", s.samplingSeq)
	if s.samplingPending == nil {
		s.samplingPending = map[string]chan samplingReply{}
	}
	ch := make(chan samplingReply, 1)
	s.samplingPending[id] = ch
	s.samplingMu.Unlock()
	defer func() {
		s.samplingMu.Lock()
		delete(s.samplingPending, id)
		s.samplingMu.Unlock()
	}()

	messages := []map[string]any{
		{"role": "user", "content": map[string]any{"type": "text", "text": user}},
	}
	params := map[string]any{"messages": messages}
	if system != "" {
		params["systemPrompt"] = system
	}
	if opts.MaxTokens > 0 {
		params["maxTokens"] = opts.MaxTokens
	}
	if opts.Temperature != 0 {
		params["temperature"] = opts.Temperature
	}
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "sampling/createMessage",
		"params":  params,
	}
	if err := s.write(req); err != nil {
		return "", fmt.Errorf("mcp: send sampling request: %w", err)
	}

	timeout := samplingTimeout
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining < timeout {
			timeout = remaining
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("mcp: host sampling cancelled: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("mcp: host sampling timed out after %s", timeout)
	case rep := <-ch:
		if rep.err != nil {
			return "", rep.err
		}
		content, ok := rep.result["content"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("mcp: sampling response missing content")
		}
		text, _ := content["text"].(string)
		if text == "" {
			return "", fmt.Errorf("mcp: sampling response has no text")
		}
		return text, nil
	}
}

// deliverSamplingReply routes a client response (a JSON-RPC message with an
// id and a result/error, no method) to the sampler waiting on that id. It
// returns false when no sampler is waiting, so the caller can fall through
// to normal request handling (a response to an unknown id is dropped).
func (s *Server) deliverSamplingReply(req rpcRequest) bool {
	k := idKey(req.ID)
	s.samplingMu.Lock()
	ch, ok := s.samplingPending[k]
	s.samplingMu.Unlock()
	if !ok {
		return false
	}
	rep := samplingReply{}
	if rawPresent(req.Error) {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(req.Error, &e) == nil {
			rep.err = fmt.Errorf("mcp: host sampling error %d: %s", e.Code, e.Message)
		} else {
			rep.err = fmt.Errorf("mcp: host sampling error (unparseable)")
		}
	} else if rawPresent(req.Result) {
		_ = json.Unmarshal(req.Result, &rep.result)
		if rep.result == nil {
			rep.err = fmt.Errorf("mcp: host sampling empty result")
		}
	} else {
		rep.err = fmt.Errorf("mcp: host sampling response with neither result nor error")
	}
	ch <- rep
	return true
}

// rawPresent reports whether a raw JSON field is present and not JSON null.
func rawPresent(raw json.RawMessage) bool {
	s := string(raw)
	return s != "" && s != "null"
}
