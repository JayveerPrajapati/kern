package llm

import (
	"context"
	"time"
)

// Embedder adapts a provider-neutral [Provider] to the single-method embedder
// signature used by docsearch.Embedder and intel.SymbolEmbedder (EmbedText).
// Embedding is Ollama-local by default; the factory can back it with a remote
// OpenAI-compatible embedder via the environment.
type Embedder struct {
	p      Provider
	ollama *OllamaProvider
}

// NewEmbedder returns an embedder backed by the provider the factory selects.
func NewEmbedder() *Embedder {
	p, err := NewProvider()
	if err != nil {
		p = NewOllamaProvider()
	}
	if op, ok := p.(*OllamaProvider); ok {
		return &Embedder{p: p, ollama: op}
	}
	return &Embedder{p: p}
}

// embedTimeout bounds a single embedding HTTP call so a multi-thousand-chunk
// index can never block indefinitely behind a hung embedder.
const embedTimeout = 20 * time.Second

// EmbedText embeds a single text. Errors are returned only when the backing
// provider actually fails; callers keep their deterministic fallback. Each call
// is bound by embedTimeout so an unresponsive server cannot stall a caller for
// chunks×60s.
func (e *Embedder) EmbedText(text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), embedTimeout)
	defer cancel()
	return e.p.Embed(ctx, text)
}

// Available reports whether the backing embedder can be reached. For the local
// Ollama backend it probes the server (matching the historical CLI check); for
// a remote provider it reports true (the caller assumes configuration intent).
func (e *Embedder) Available() bool {
	if e.ollama != nil {
		return e.ollama.client.Available()
	}
	return true
}

// HasEmbeddingModel reports whether the embedding model is installed/usable.
// For Ollama it checks /api/tags; for remote providers it reports true.
func (e *Embedder) HasEmbeddingModel() bool {
	if e.ollama != nil {
		return e.ollama.client.HasEmbeddingModel()
	}
	return true
}
