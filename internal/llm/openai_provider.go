package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OpenAICompatibleProvider speaks the OpenAI chat/completions + embeddings wire
// format against a configurable base URL and API key. One adapter covers OpenAI
// plus any OpenAI-compatible host (OpenRouter, Groq, LiteLLM, vLLM).
// Env: OPENAI_BASE_URL (default https://api.openai.com/v1), OPENAI_API_KEY,
// OPENAI_MODEL, OPENAI_EMBED_MODEL.
type OpenAICompatibleProvider struct {
	BaseURL    string
	APIKey     string
	Model      string
	EmbedModel string
	HTTP       *http.Client
}

// NewOpenAIProvider builds the provider from the environment. It validates
// OPENAI_BASE_URL (https-only and no private/loopback/link-local targets, save
// localhost/127.0.0.1 for local testing) and errors on a rejected value.
func NewOpenAIProvider() (*OpenAICompatibleProvider, error) {
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if err := ValidateBaseURL(base); err != nil {
		return nil, err
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	embed := os.Getenv("OPENAI_EMBED_MODEL")
	if embed == "" {
		embed = "text-embedding-3-small"
	}
	return &OpenAICompatibleProvider{
		BaseURL:    strings.TrimRight(base, "/"),
		APIKey:     os.Getenv("OPENAI_API_KEY"),
		Model:      model,
		EmbedModel: embed,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// ValidateBaseURL enforces a safe OPENAI_BASE_URL. It requires an https scheme
// unless the host is localhost/127.0.0.1 (for local testing), and rejects
// private, loopback and link-local IP targets. Together these block SSRF
// against internal metadata endpoints (e.g. 169.254.169.254) and prevent the
// Bearer key from being sent in cleartext over http.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("llm: invalid OPENAI_BASE_URL %q: %v", raw, err)
	}
	host := u.Hostname()
	allowedLocal := host == "localhost" || host == "127.0.0.1"
	if u.Scheme != "https" && !allowedLocal {
		return fmt.Errorf("llm: OPENAI_BASE_URL %q must use https (http is allowed only for localhost/127.0.0.1)", raw)
	}
	if ip := net.ParseIP(host); ip != nil && !allowedLocal &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("llm: OPENAI_BASE_URL %q targets a private or loopback address %q", raw, host)
	}
	return nil
}

func (o *OpenAICompatibleProvider) model(opts Options) string {
	if opts.Model != "" {
		return opts.Model
	}
	return o.Model
}

// Generate implements Provider via /chat/completions.
func (o *OpenAICompatibleProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	body := map[string]any{
		"model":    o.model(opts),
		"messages": chatMessages(system, user),
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != 0 {
		body["temperature"] = opts.Temperature
	}
	resp, err := o.post(ctx, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: empty choices")
	}
	txt := strings.TrimSpace(out.Choices[0].Message.Content)
	if txt == "" {
		return "", fmt.Errorf("openai: empty completion")
	}
	return txt, nil
}

// Embed implements Provider via /embeddings.
func (o *OpenAICompatibleProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	body := map[string]any{"model": o.EmbedModel, "input": text}
	resp, err := o.post(ctx, "/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embed status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("openai: empty embedding")
	}
	return out.Data[0].Embedding, nil
}

// Capabilities reports generate+embed+stream.
func (o *OpenAICompatibleProvider) Capabilities() Capabilities {
	return Capabilities{Generate: true, Embed: true, Stream: true, Models: []string{o.Model, o.EmbedModel}}
}

// Stream implements Provider via /chat/completions with stream=true (SSE).
func (o *OpenAICompatibleProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	body := map[string]any{
		"model":    o.model(opts),
		"messages": chatMessages(system, user),
		"stream":   true,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != 0 {
		body["temperature"] = opts.Temperature
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	o.setHeaders(req)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("openai status %d", resp.StatusCode)
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
			if line == "[DONE]" {
				return
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(line), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 {
				if _, werr := pw.Write([]byte(chunk.Choices[0].Delta.Content)); werr != nil {
					return
				}
			}
		}
	}()
	// Close unblocks the pump on two fronts: resp.Body.Close() aborts
	// sc.Scan(), and pw.Close() aborts an in-flight pw.Write() that is
	// stalled because the consumer stopped reading a full pipe buffer.
	// Without the pw.Close(), a pump blocked mid-write survives Close()
	// and leaks. Double-close of an io.Pipe is a harmless ErrClosedPipe.
	return &Stream{Reader: pr, Close: func() error {
		resp.Body.Close()
		pw.Close()
		return nil
	}}, nil
}

func (o *OpenAICompatibleProvider) post(ctx context.Context, path string, payload map[string]any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	o.setHeaders(req)
	return o.HTTP.Do(req)
}

func (o *OpenAICompatibleProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
}

func chatMessages(system, user string) []map[string]string {
	messages := []map[string]string{{"role": "system", "content": system}}
	if user != "" {
		messages = append(messages, map[string]string{"role": "user", "content": user})
	}
	return messages
}
