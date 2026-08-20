package agent

import (
	"context"
	"time"

	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// Provider is the model provider abstraction. Callers inject a [Provider] so
// the runtime never talks to an external model directly.
type Provider interface {
	// Generate completes a prompt and returns the text response.
	Generate(prompt string, options ...Option) (string, error)
}

// Option configures a generate call.
type Option func(*Request)

// Request is a generate request.
type Request struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

// WithModel sets the model.
func WithModel(m string) Option {
	return func(r *Request) { r.Model = m }
}

// WithMaxTokens sets the token cap.
func WithMaxTokens(n int) Option {
	return func(r *Request) { r.MaxTokens = n }
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float64) Option {
	return func(r *Request) { r.Temperature = t }
}

// ApplyOptions folds options into a fresh Request.
func ApplyOptions(options []Option) Request {
	r := Request{}
	for _, opt := range options {
		opt(&r)
	}
	return r
}

// ollamaProvider adapts internal/llm to the [Provider] interface. The backing
// vendor is selected via KERN_LLM_PROVIDER (default ollama), so the runtime is
// not locked to one backend.
type ollamaProvider struct {
	p llm.Provider
}

// Generate implements Provider, mapping options onto [llm.Options].
func (o *ollamaProvider) Generate(prompt string, options ...Option) (string, error) {
	req := ApplyOptions(options)
	start := time.Now()
	res, err := o.p.Generate(context.Background(), "", prompt, llm.Options{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	metrics.Default().RecordLLMLatency(time.Since(start))
	return res, err
}

// OllamaProvider returns a [Provider] backed by the provider-neutral factory.
// The vendor is chosen by KERN_LLM_PROVIDER and defaults to the local Ollama
// client. It is never nil.
func OllamaProvider() Provider {
	p, err := llm.NewProvider()
	if err != nil {
		p = llm.NewOllamaProvider()
	}
	return &ollamaProvider{p: p}
}
