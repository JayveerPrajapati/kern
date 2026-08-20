package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OllamaProvider is the local-first default provider. It wraps the existing
// [Client] (POST /api/generate, /api/embed) and honors Options: the model
// override, MaxTokens (num_predict), Temperature and Seed are carried in the
// Ollama "options" block.
type OllamaProvider struct {
	client *Client
}

// NewOllamaProvider returns the local Ollama provider.
func NewOllamaProvider() *OllamaProvider {
	return &OllamaProvider{client: New("")}
}

// model resolves the effective model: an Options override wins, else the
// client's configured model.
func (o *OllamaProvider) model(opts Options) string {
	if opts.Model != "" {
		return opts.Model
	}
	return o.client.Model
}

// Generate implements Provider against Ollama's /api/generate.
func (o *OllamaProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	payload := map[string]any{
		"model":  o.model(opts),
		"prompt": joinPrompt(system, user),
		"stream": false,
	}
	if oo := ollamaOptions(opts); len(oo) > 0 {
		payload["options"] = oo
	}
	resp, err := o.post(ctx, "/api/generate", payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	out.Response = strings.TrimSpace(out.Response)
	if out.Response == "" {
		return "", errors.New("empty ollama response")
	}
	return out.Response, nil
}

// Embed implements Provider via /api/embed. The context lets the caller abort
// an embedding (a per-call timeout still guards against a hung server).
func (o *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return o.client.EmbedText(ctx, text)
}

// Capabilities reports full support (generate, embed, stream).
func (o *OllamaProvider) Capabilities() Capabilities {
	return Capabilities{Generate: true, Embed: true, Stream: true, Models: []string{o.client.Model}}
}

// Stream implements Provider via /api/generate with stream=true, yielding
// NDJSON "response" tokens.
func (o *OllamaProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	payload := map[string]any{
		"model":  o.model(opts),
		"prompt": joinPrompt(system, user),
		"stream": true,
	}
	if oo := ollamaOptions(opts); len(oo) > 0 {
		payload["options"] = oo
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.client.Base+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()
		dec := json.NewDecoder(resp.Body)
		for {
			var chunk struct {
				Response string `json:"response"`
			}
			if err := dec.Decode(&chunk); err != nil {
				return
			}
			if _, werr := pw.Write([]byte(chunk.Response)); werr != nil {
				return
			}
		}
	}()
	// Close unblocks the pump on two fronts: resp.Body.Close() aborts
	// dec.Decode(), and pw.Close() aborts an in-flight pw.Write() that is
	// stalled because the consumer stopped reading a full pipe buffer.
	// Without the pw.Close(), a pump blocked mid-write survives Close()
	// and leaks. Double-close of an io.Pipe is a harmless ErrClosedPipe.
	return &Stream{Reader: pr, Close: func() error {
		resp.Body.Close()
		pw.Close()
		return nil
	}}, nil
}

func (o *OllamaProvider) post(ctx context.Context, path string, payload map[string]any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.client.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return o.client.HTTP.Do(req)
}

// joinPrompt folds a system and user prompt as "system\n\nuser", preserving the
// existing wire contract.
func joinPrompt(system, user string) string {
	if system == "" {
		return user
	}
	if user == "" {
		return system
	}
	return system + "\n\n" + user
}

// ollamaOptions maps provider Options into Ollama's "options" block. Returns
// nil when no field is set so the payload is unchanged for default calls.
func ollamaOptions(opts Options) map[string]any {
	oo := map[string]any{}
	if opts.MaxTokens > 0 {
		oo["num_predict"] = opts.MaxTokens
	}
	if opts.Temperature != 0 {
		oo["temperature"] = opts.Temperature
	}
	if opts.Seed != nil {
		oo["seed"] = *opts.Seed
	}
	if len(oo) == 0 {
		return nil
	}
	return oo
}
