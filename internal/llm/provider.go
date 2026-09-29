package llm

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// Options configures a single generation call across every provider. It is
// provider-independent: no vendor field is exposed, and zero values mean "use
// the provider's default".
type Options struct {
	Model       string  // model override; empty = provider default
	MaxTokens   int     // token cap; 0 = provider default
	Temperature float64 // sampling temperature; 0 = provider default
	Seed        *int    // optional deterministic seed
}

// Capabilities describes what a provider can do. Callers use it to degrade
// gracefully (e.g. skip embedding when Embed is false) without naming a vendor.
type Capabilities struct {
	Generate bool
	Embed    bool
	Stream   bool
	Models   []string // known model names, when the provider exposes them
}

// Supports reports whether the provider can apply opts. Providers that lack a
// field may silently ignore it; this is a strategy signal, not a guard.
func (c Capabilities) Supports(opts Options) bool {
	return true
}

// Stream is a live token stream from a provider. Reader yields text tokens and
// returns io.EOF at the end. Close releases the underlying connection.
type Stream struct {
	Reader io.Reader
	Close  func() error
}

// Provider is the provider-neutral model interface. Kern talks only to this
// interface; the factory (NewProvider) selects a concrete provider once.
type Provider interface {
	// Generate completes a system+user prompt and returns the model's text.
	Generate(ctx context.Context, system, user string, opts Options) (string, error)
	// Embed returns a dense embedding for text. Providers without embeddings
	// (see Capabilities.Embed) return an error.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Capabilities reports what this provider supports.
	Capabilities() Capabilities
	// Stream returns a token stream for system+user. Providers without
	// streaming (Capabilities.Stream) return an error.
	Stream(ctx context.Context, system, user string, opts Options) (*Stream, error)
}

// CompressInstruction is the standard system prompt for context compression.
const CompressInstruction = "You are a context optimizer for an AI coding assistant. Compress the following prompt: keep the intent, constraints, file paths and key identifiers, remove fluff and unnecessary details. Reply with only the compressed prompt and no commentary."

// CompressVia condenses prompt through a provider-neutral [Provider] using the
// standard compression instruction. It is the provider-agnostic replacement
// for Client.Compress: callers keep their deterministic fallback when it
// errors or returns empty.
func CompressVia(ctx context.Context, p Provider, prompt string, opts Options) (string, error) {
	if p == nil {
		return "", fmt.Errorf("llm: nil provider")
	}
	return p.Generate(ctx, CompressInstruction, prompt, opts)
}

// ProviderName returns the provider selected by KERN_LLM_PROVIDER ("auto"
// when unset) — the label `kern agents` prints for the active selection.
func ProviderName() string { return providerName() }

// providerName returns the provider selected by KERN_LLM_PROVIDER (or
// llm.provider in .kern/config.json; default "ollama"). It is the single
// place that maps the config to a vendor.
func providerName() string {
	if n := config.String("", "KERN_LLM_PROVIDER", "llm.provider", "auto"); n != "" {
		return strings.ToLower(n)
	}
	return "auto"
}

// NewProvider builds the provider selected by KERN_LLM_PROVIDER
// (ollama|openai|anthropic|google; default "auto" — the chain). Per-provider
// credentials come from their own env vars. It errors only when a non-default
// provider is selected but its API key is missing. Construction is
// deterministic; network is touched only when a provider method is invoked.
func NewProvider() (Provider, error) {
	switch providerName() {
	case "mcp", "host", "sampling":
		return NewMCPProvider(), nil
	case "openai", "openrouter", "groq", "litellm", "vllm", "azure":
		return NewOpenAIProvider()
	case "anthropic":
		return NewAnthropicProvider()
	case "google", "gemini":
		return NewGoogleProvider()
	case "claude", "codex", "gemini-cli", "qwen", "agy", "antigravity":
		return NewLocalCliProvider(strings.TrimSuffix(providerName(), "-cli")), nil
	case "auto":
		// auto: active MCP host sampler (when connected) — the CURRENT
		// session's model, already wired, no new session spun up — then the
		// locally-installed agent CLIs (claude, opencode, codex, gemini, qwen,
		// agy, antigravity), then Ollama last. The host-first order is the
		// "MCP ack" rule: when kern is hosted inside an agent session, that
		// same session/model does the task instead of spawning a fresh one.
		// Multiple sessions coexist (each registers its own slot, tried in
		// registration order). Construction never touches the network; a dead
		// provider fails fast at Generate time and the chain moves on.
		var chain []Provider
		if HasHostSampler() {
			chain = append(chain, NewMCPProvider())
		}
		for _, name := range AvailableLocalAgents() {
			chain = append(chain, NewLocalCliProvider(name))
		}
		chain = append(chain, NewOllamaProvider())
		return NewChainProvider(chain...), nil
	default:
		return NewOllamaProvider(), nil
	}
}

// ProbeReachable verifies a reachable LLM provider before a command that
// hard-depends on one (kern do / kern_loop autonomous). It is the shared,
// capability-aware probe used by both the CLI (cmd/kern/helpers.go
// probeLLMProvider) and the MCP server (internal/mcp/highlevel/highlevel.go
// ProbeLLMProviderReachable), so the two surfaces cannot drift.
//
// The probe mirrors the auto chain's order but probes each leg with a budget
// matched to its nature instead of one flat short deadline (dogfooding G-HIGH:
// an 8s flat budget was a coin-flip against CLI cold-starts of 6-40s):
//
//  1. active MCP host session (the "MCP ack": kern knows the session and
//     model, and the SAME session does the task — no new session spun up);
//  2. locally-installed agent CLIs (claude, opencode, codex, ...) in
//     preference order;
//  3. Ollama last (fails fast in milliseconds when down).
//
// A dead leg is skipped, not fatal, so a working fallback is still found; the
// aggregated error names every leg that was tried.
func ProbeReachable() error {
	_, err := ProbeReachableName()
	return err
}

// ProbeReachableName is ProbeReachable plus the identity of the provider that
// answered. The auto chain falls back across providers (host session → agent
// CLIs → Ollama), so a successful probe alone does not tell the caller which
// leg actually answered — this returns that name so `kern do` can report
// "provider: claude" instead of silently running on an unspecified fallback
// (dogfooding E-obs).
func ProbeReachableName() (string, error) {
	if n := providerName(); n != "auto" {
		// Explicit provider (openai/anthropic/google/...): probe it directly.
		prov, err := NewProvider()
		if err != nil {
			return "", err
		}
		if err := probeLeg(prov, 30*time.Second); err != nil {
			return "", err
		}
		return n, nil
	}
	var tried []string
	if HasHostSampler() {
		if err := probeLeg(NewMCPProvider(), 15*time.Second); err == nil {
			return "host (MCP session)", nil
		} else {
			tried = append(tried, "host: "+err.Error())
		}
	}
	for _, name := range AvailableLocalAgents() {
		if err := probeLeg(NewLocalCliProvider(name), 45*time.Second); err == nil {
			return name, nil
		} else {
			tried = append(tried, name+": "+err.Error())
		}
	}
	if err := probeLeg(NewOllamaProvider(), 10*time.Second); err == nil {
		return "ollama", nil
	} else {
		tried = append(tried, "ollama: "+err.Error())
	}
	return "", fmt.Errorf("no reachable LLM provider (%s)", strings.Join(tried, "; "))
}

// probeLeg asks one provider a trivial question under a bounded deadline. It
// returns nil when the provider answered non-empty, else the provider error.
func probeLeg(prov Provider, budget time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	out, err := prov.Generate(ctx, "", "Reply with exactly: OK", Options{})
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("provider returned an empty response")
	}
	return nil
}

// MaskRequired reports whether the provider selected by KERN_LLM_PROVIDER sends
// prompts to a machine other than the local one. Remote providers
// (openai/anthropic/google) are always remote; the local Ollama provider is
// remote only when OLLAMA_HOST points at a non-local host. Callers use this to
// decide whether to PII-mask a prompt before it leaves the machine.
func MaskRequired() bool {
	p, err := NewProvider()
	if err != nil {
		return false
	}
	if chain, ok := p.(*ChainProvider); ok {
		return !chain.AllLocal()
	}
	if _, isOllama := p.(*OllamaProvider); isOllama {
		return !isLocalHost(New("").Base)
	}
	if _, isLocal := p.(*LocalCliProvider); isLocal {
		return false // agent CLIs run on this machine
	}
	if _, isMCP := p.(*MCPProvider); isMCP {
		return false // MCP host agent runs in the operator's active session
	}
	return true
}

// isLocalHost reports whether base (an Ollama base URL) points at the local
// machine. Anything else (LAN IP, remote host, tunnel) is treated as non-local.
func isLocalHost(base string) bool {
	host := base
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
