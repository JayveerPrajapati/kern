package llm

import (
	"context"
	"fmt"
	"sync"
)

// Sampler is a function that delegates text generation to an active MCP host agent
// or sampling client (e.g. Antigravity, Claude Code, Cursor) via MCP sampling.
type Sampler func(ctx context.Context, system, user string, opts Options) (string, error)

var (
	hostSamplerMu sync.RWMutex
	hostSampler   Sampler
)

// RegisterHostSampler registers a host sampling handler from the active MCP host session.
// In accordance with the effect registration principle, it returns a disposer function
// that unregisters the handler when called.
func RegisterHostSampler(s Sampler) func() {
	hostSamplerMu.Lock()
	hostSampler = s
	hostSamplerMu.Unlock()
	return func() {
		hostSamplerMu.Lock()
		if hostSampler != nil {
			hostSampler = nil
		}
		hostSamplerMu.Unlock()
	}
}

// HasHostSampler reports whether an active host MCP sampler is currently registered.
func HasHostSampler() bool {
	hostSamplerMu.RLock()
	defer hostSamplerMu.RUnlock()
	return hostSampler != nil
}

// MCPProvider implements the [Provider] interface by delegating generation to
// an active MCP host agent via sampling (sampling/createMessage) or a direct Sampler callback.
// This enables Kern to leverage the active host model's frontier intelligence without
// requiring local Ollama daemons or separate API keys.
type MCPProvider struct {
	sampler Sampler
}

// NewMCPProvider creates an MCPProvider. If a sampler is provided, it is bound to this
// instance; otherwise, Generate uses the globally registered host sampler.
func NewMCPProvider(sampler ...Sampler) *MCPProvider {
	var s Sampler
	if len(sampler) > 0 && sampler[0] != nil {
		s = sampler[0]
	}
	return &MCPProvider{sampler: s}
}

// Generate completes a prompt using the MCP host agent.
func (p *MCPProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	s := p.sampler
	if s == nil {
		hostSamplerMu.RLock()
		s = hostSampler
		hostSamplerMu.RUnlock()
	}
	if s == nil {
		return "", fmt.Errorf("llm: mcp host sampler is not available (no active MCP sampling connection; set KERN_LLM_PROVIDER or start Ollama)")
	}
	return s(ctx, system, user, opts)
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
