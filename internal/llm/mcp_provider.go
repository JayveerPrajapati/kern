package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Sampler is a function that delegates text generation to an active MCP host agent
// or sampling client (e.g. Antigravity, Claude Code, Cursor) via MCP sampling.
type Sampler func(ctx context.Context, system, user string, opts Options) (string, error)

// samplerEntry is one registered host sampler. Multiple entries coexist —
// one per connected host/session/agent — and are tried in registration order
// (first success wins), so a process serving several clients (multi-root
// serveMany, several wired agents) delegates to any of them. instance tags a
// specific registration so a stale disposer can never remove a newer
// registration that replaced it under the same key.
type samplerEntry struct {
	key      string
	instance int64
	s        Sampler
}

var (
	hostSamplerMu  sync.Mutex
	hostSamplers   []samplerEntry
	hostSamplerSeq int64
)

// RegisterHostSampler registers a host sampling handler from the active MCP host session
// under the "default" slot (replacing any previous default). In accordance with the
// effect registration principle, it returns a disposer function
// that unregisters the handler when called.
func RegisterHostSampler(s Sampler) func() {
	return RegisterHostSamplerFor("default", s)
}

// RegisterHostSamplerFor registers a host sampling handler under a caller-
// chosen key, so several sessions/agents/repos can coexist: each key is an
// independent slot and the chain tries every registered sampler in
// registration order. Re-registering the same key replaces that slot (its
// position is kept); a disposer from a REPLACED registration is a no-op, so
// callers may safely dispose the old handle after re-registering. Returns a
// disposer that removes only this registration.
func RegisterHostSamplerFor(key string, s Sampler) func() {
	hostSamplerMu.Lock()
	defer hostSamplerMu.Unlock()
	inst := hostSamplerSeq + 1
	hostSamplerSeq = inst
	for i, e := range hostSamplers {
		if e.key == key {
			hostSamplers[i] = samplerEntry{key: key, instance: inst, s: s} // replace in place, keep registration order
			return func() { removeSamplerInstance(key, inst) }
		}
	}
	hostSamplers = append(hostSamplers, samplerEntry{key: key, instance: inst, s: s})
	return func() { removeSamplerInstance(key, inst) }
}

// removeSamplerInstance removes the registration matching both key and
// instance — a stale disposer (from a registration that was replaced) is a
// no-op and cannot delete the newer registration.
func removeSamplerInstance(key string, inst int64) {
	hostSamplerMu.Lock()
	defer hostSamplerMu.Unlock()
	for i, e := range hostSamplers {
		if e.key == key && e.instance == inst {
			hostSamplers = append(hostSamplers[:i], hostSamplers[i+1:]...)
			return
		}
	}
}

// HasHostSampler reports whether any host MCP sampler is currently registered.
func HasHostSampler() bool {
	hostSamplerMu.Lock()
	defer hostSamplerMu.Unlock()
	return len(hostSamplers) > 0
}

// registeredSamplers returns a snapshot of the currently registered samplers
// in registration order.
func registeredSamplers() []Sampler {
	hostSamplerMu.Lock()
	defer hostSamplerMu.Unlock()
	out := make([]Sampler, 0, len(hostSamplers))
	for _, e := range hostSamplers {
		out = append(out, e.s)
	}
	return out
}

// MCPProvider implements the [Provider] interface by delegating generation to
// an active MCP host agent via sampling (sampling/createMessage) or a direct Sampler callback.
// This enables Kern to leverage the active host model's frontier intelligence without
// requiring local Ollama daemons or separate API keys. When multiple samplers are
// registered (several sessions/agents), each is tried in registration order.
type MCPProvider struct {
	sampler Sampler
}

// NewMCPProvider creates an MCPProvider. If a sampler is provided, it is bound to this
// instance; otherwise, Generate uses the globally registered host samplers.
func NewMCPProvider(sampler ...Sampler) *MCPProvider {
	var s Sampler
	if len(sampler) > 0 && sampler[0] != nil {
		s = sampler[0]
	}
	return &MCPProvider{sampler: s}
}

// Generate completes a prompt using the MCP host agent(s): the bound sampler
// when one was given, else every registered host sampler in order (first
// success wins). The aggregated error names each sampler's failure.
func (p *MCPProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	samplers := make([]Sampler, 0, 1)
	if p.sampler != nil {
		samplers = append(samplers, p.sampler)
	} else {
		samplers = registeredSamplers()
	}
	if len(samplers) == 0 {
		return "", fmt.Errorf("llm: mcp host sampler is not available (no active MCP sampling connection; set KERN_LLM_PROVIDER or start Ollama)")
	}
	var errs []string
	for _, s := range samplers {
		out, err := s(ctx, system, user, opts)
		if err == nil && out != "" {
			return out, nil
		}
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			errs = append(errs, "empty output")
		}
	}
	return "", fmt.Errorf("llm: all host samplers failed (%s)", strings.Join(errs, "; "))
}

// Embed returns an error because MCP sampling does not provide an embedding endpoint.
func (p *MCPProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("llm: mcp provider: embeddings not supported over MCP sampling")
}

// Stream returns an error because basic MCP sampling returns complete messages.
func (p *MCPProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	return nil, fmt.Errorf("llm: mcp provider: streaming not supported over MCP sampling")
}

// Capabilities reports that MCPProvider supports generation only.
func (p *MCPProvider) Capabilities() Capabilities {
	return Capabilities{
		Generate: true,
		Embed:    false,
		Stream:   false,
	}
}
