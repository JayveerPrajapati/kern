package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// AnthropicProvider speaks the Anthropic Messages API. Generate only — it has
// no embedding endpoint, so Capabilities.Embed=false.
// Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL (default claude-sonnet-4-5).
type AnthropicProvider struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// NewAnthropicProvider builds the provider, requiring ANTHROPIC_API_KEY.
func NewAnthropicProvider() (*AnthropicProvider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("llm: ANTHROPIC_API_KEY is not set (KERN_LLM_PROVIDER=anthropic)")
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	return &AnthropicProvider{APIKey: key, Model: model, BaseURL: "https://api.anthropic.com", HTTP: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (o *AnthropicProvider) model(opts Options) string {
	if opts.Model != "" {
		return opts.Model
	}
	return o.Model
}

// Generate implements Provider via POST /v1/messages.
func (o *AnthropicProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024 // Anthropic requires an explicit max_tokens.
	}
	body := map[string]any{
		"model":      o.model(opts),
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	}
	if system != "" {
		body["system"] = system
	}
	if opts.Temperature != 0 {
		body["temperature"] = opts.Temperature
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", o.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic status %d", resp.StatusCode)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	txt := strings.TrimSpace(b.String())
	if txt == "" {
		return "", fmt.Errorf("anthropic: empty completion")
	}
	return txt, nil
}

// Embed is unsupported: Anthropic has no embeddings endpoint.
func (o *AnthropicProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("llm: anthropic does not support embeddings")
}

// Capabilities reports generate-only (no embed, no stream).
func (o *AnthropicProvider) Capabilities() Capabilities {
	return Capabilities{Generate: true, Embed: false, Stream: false, Models: []string{o.Model}}
}

// Stream is unsupported for this provider.
func (o *AnthropicProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	return nil, fmt.Errorf("llm: anthropic streaming not supported")
}
