package llm

import (
	"context"
	"fmt"
	"strings"
)

// ChainProvider tries a list of providers in order and returns the first
// successful result. It is the "auto" mode: when Ollama is not running, the
// next locally-available agent CLI (claude, codex, ...) serves the call.
type ChainProvider struct {
	providers []Provider
	names     []string
}

// NewChainProvider builds a chain over the given providers.
func NewChainProvider(providers ...Provider) *ChainProvider {
	c := &ChainProvider{providers: providers}
	for _, p := range providers {
		c.names = append(c.names, providerNameOf(p))
	}
	return c
}

// Providers exposes the chained providers (for tests and introspection).
func (c *ChainProvider) Providers() []Provider { return c.providers }

// Generate tries each provider in order; the first success wins. When every
// provider fails, the error names each one's failure so callers (and users)
// see exactly what was tried — the opposite of a silent no-op.
func (c *ChainProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	if len(c.providers) == 0 {
		return "", fmt.Errorf("llm: no provider available in the chain")
	}
	var errs []string
	for i, p := range c.providers {
		if ctx.Err() != nil {
			break
		}
		out, err := p.Generate(ctx, system, user, opts)
		if err == nil && out != "" {
			return out, nil
		}
		label := c.names[i]
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", label, err))
		} else {
			errs = append(errs, fmt.Sprintf("%s: empty output", label))
		}
	}
	return "", fmt.Errorf("llm: all providers failed (%s)", strings.Join(errs, "; "))
}

// Embed tries each provider in order (only providers with embedding
// capability can succeed).
func (c *ChainProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	var errs []string
	for i, p := range c.providers {
		if !p.Capabilities().Embed {
			continue
		}
		emb, err := p.Embed(ctx, text)
		if err == nil {
			return emb, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", c.names[i], err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("llm: no provider in the chain supports embeddings")
	}
	return nil, fmt.Errorf("llm: all embedding providers failed (%s)", strings.Join(errs, "; "))
}

// EmbedBatch embeds many texts via the first chained provider that supports
// batched embedding (Ollama's /api/embed input array). The auto chain leads
// with Ollama, so a multi-thousand-chunk index costs one HTTP round-trip
// instead of thousands. When no chained provider batches, the error makes the
// caller fall back to per-text embedding.
func (c *ChainProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var errs []string
	for i, p := range c.providers {
		bp, ok := p.(batchProvider)
		if !ok {
			continue
		}
		vecs, err := bp.EmbedBatch(ctx, texts)
		if err == nil {
			return vecs, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", c.names[i], err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("llm: no provider in the chain supports batched embeddings")
	}
	return nil, fmt.Errorf("llm: all batched embedding providers failed (%s)", strings.Join(errs, "; "))
}

// Stream tries each provider in order.
func (c *ChainProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	var errs []string
	for i, p := range c.providers {
		if !p.Capabilities().Stream {
			continue
		}
		s, err := p.Stream(ctx, system, user, opts)
		if err == nil {
			return s, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", c.names[i], err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("llm: no provider in the chain supports streaming")
	}
	return nil, fmt.Errorf("llm: all streaming providers failed (%s)", strings.Join(errs, "; "))
}

// Capabilities is the union of the chained providers.
func (c *ChainProvider) Capabilities() Capabilities {
	caps := Capabilities{}
	for _, p := range c.providers {
		pc := p.Capabilities()
		caps.Generate = caps.Generate || pc.Generate
		caps.Embed = caps.Embed || pc.Embed
		caps.Stream = caps.Stream || pc.Stream
		caps.Models = append(caps.Models, pc.Models...)
	}
	return caps
}

// AllLocal reports whether every provider in the chain is a local-machine
// provider (local Ollama or a local agent CLI) — i.e. prompts never leave the
// machine, so PII masking is unnecessary.
func (c *ChainProvider) AllLocal() bool {
	for _, p := range c.providers {
		switch pp := p.(type) {
		case *OllamaProvider:
			if !isLocalHost(New("").Base) {
				return false
			}
		case *LocalCliProvider:
			// agent CLIs run on this machine (pp intentionally unused)
			_ = pp
		case *MCPProvider:
			// MCP host agent runs in the operator's active session
			_ = pp
		default:
			return false
		}
	}
	return true
}

func providerNameOf(p Provider) string {
	switch pp := p.(type) {
	case *MCPProvider:
		return "host"
	case *OllamaProvider:
		return "ollama"
	case *LocalCliProvider:
		return pp.name
	case *OpenAICompatibleProvider:
		return "openai"
	case *AnthropicProvider:
		return "anthropic"
	case *GoogleProvider:
		return "google"
	}
	return fmt.Sprintf("%T", p)
}
