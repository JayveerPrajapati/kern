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

// GoogleProvider speaks the Gemini generateContent API. Generate only — no
// embedding endpoint, so Capabilities.Embed=false.
// Env: GEMINI_API_KEY, GEMINI_MODEL (default gemini-2.5-flash).
type GoogleProvider struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// NewGoogleProvider builds the provider, requiring GEMINI_API_KEY.
func NewGoogleProvider() (*GoogleProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("llm: GEMINI_API_KEY is not set (KERN_LLM_PROVIDER=google)")
	}
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &GoogleProvider{APIKey: key, Model: model, BaseURL: "https://generativelanguage.googleapis.com", HTTP: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (o *GoogleProvider) model(opts Options) string {
	if opts.Model != "" {
		return opts.Model
	}
	return o.Model
}

// Generate implements Provider via :generateContent.
func (o *GoogleProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": user}}},
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": system}}}
	}
	if opts.MaxTokens > 0 || opts.Temperature != 0 {
		gc := map[string]any{}
		if opts.MaxTokens > 0 {
			gc["maxOutputTokens"] = opts.MaxTokens
		}
		if opts.Temperature != 0 {
			gc["temperature"] = opts.Temperature
		}
		body["generationConfig"] = gc
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent",
		o.BaseURL, o.model(opts))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// The API key travels in the x-goog-api-key header, not the URL query
	// string, so it never appears in access/proxy logs or network inspection.
	req.Header.Set("x-goog-api-key", o.APIKey)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google status %d", resp.StatusCode)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("google: empty candidates")
	}
	txt := strings.TrimSpace(out.Candidates[0].Content.Parts[0].Text)
	if txt == "" {
		return "", fmt.Errorf("google: empty completion")
	}
	return txt, nil
}

// Embed is unsupported.
func (o *GoogleProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("llm: google does not support embeddings")
}

// Capabilities reports generate-only.
func (o *GoogleProvider) Capabilities() Capabilities {
	return Capabilities{Generate: true, Embed: false, Stream: false, Models: []string{o.Model}}
}

// Stream is unsupported for this provider.
func (o *GoogleProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	return nil, fmt.Errorf("llm: google streaming not supported")
}
