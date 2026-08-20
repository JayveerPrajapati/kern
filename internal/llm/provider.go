package llm

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
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

// providerName returns the provider selected by KERN_LLM_PROVIDER (default
// "ollama"). It is the single place that maps an env var to a vendor.
func providerName() string {
	if n := os.Getenv("KERN_LLM_PROVIDER"); n != "" {
		return strings.ToLower(n)
	}
	return "ollama"
}

// NewProvider builds the provider selected by KERN_LLM_PROVIDER
// (ollama|openai|anthropic|google; default "ollama"). Per-provider credentials
// come from their own env vars. It errors only when a non-default provider is
// selected but its API key is missing. Construction is deterministic; network
// is touched only when a provider method is invoked.
func NewProvider() (Provider, error) {
	switch providerName() {
	case "openai", "openrouter", "groq", "litellm", "vllm", "azure":
		return NewOpenAIProvider()
	case "anthropic":
		return NewAnthropicProvider()
	case "google", "gemini":
		return NewGoogleProvider()
	default:
		return NewOllamaProvider(), nil
	}
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
	if _, isOllama := p.(*OllamaProvider); isOllama {
		return !isLocalHost(New("").Base)
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
