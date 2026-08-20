// Package llm implements an optional, opt-in compression step backed by a
// local Ollama server. It is never used implicitly: callers must request it
// with an explicit model name (CLI --llm or the KERN_MODEL env var). If Ollama
// is unreachable the caller falls back to the deterministic path.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultModel is used when no model is requested.
	DefaultModel = "llama3.2"
	defaultHost  = "http://localhost:11434"
	// DefaultEmbedModel is used when no embedding model is requested.
	DefaultEmbedModel = "nomic-embed-text"
)

// Client talks to a local Ollama server.
type Client struct {
	Base  string
	Model string
	HTTP  *http.Client
}

// New builds a client. The model comes from the argument, else KERN_MODEL,
// else DefaultModel. The base URL comes from OLLAMA_HOST, else localhost.
func New(model string) *Client {
	if model == "" {
		model = os.Getenv("KERN_MODEL")
	}
	if model == "" {
		model = DefaultModel
	}
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		base = defaultHost
	}
	return &Client{
		Base:  base,
		Model: model,
		HTTP:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Available reports whether a local Ollama answers at Base.
func (c *Client) Available() bool {
	req, err := http.NewRequest(http.MethodGet, c.Base+"/api/tags", nil)
	if err != nil {
		return false
	}
	probe := &http.Client{Timeout: 2 * time.Second}
	resp, err := probe.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

const instruction = "You are a context optimizer for an AI coding assistant. Compress the following prompt: keep the intent, constraints, file paths and key identifiers, remove fluff and redundancy. Reply with only the compressed prompt and no commentary."

// Compress asks the local model to condense prompt. It returns an error when
// Ollama is unavailable, the model errors, or the response is empty.
func (c *Client) Compress(prompt string) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("ollama not reachable at %s", c.Base)
	}
	payload, err := json.Marshal(map[string]any{
		"model":  c.Model,
		"prompt": instruction + "\n\n" + prompt,
		"stream": false,
	})
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Post(c.Base+"/api/generate", "application/json", bytes.NewReader(payload))
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

// Complete asks the local model to continue a directive prompt (system) plus
// user context. It is the generic completion used by the self-correction loop
// and returns the model's raw text.
func (c *Client) Complete(system, user string) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("ollama not reachable at %s", c.Base)
	}
	payload, err := json.Marshal(map[string]any{
		"model":  c.Model,
		"prompt": system + "\n\n" + user,
		"stream": false,
	})
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Post(c.Base+"/api/generate", "application/json", bytes.NewReader(payload))
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

// EmbedModel resolves the embedding model: the argument, else KERN_EMBED_MODEL,
// else DefaultEmbedModel.
func EmbedModel() string {
	m := os.Getenv("KERN_EMBED_MODEL")
	if m == "" {
		m = DefaultEmbedModel
	}
	return m
}

// HasEmbeddingModel reports whether the embedding model (EmbedModel) is
// available in the local Ollama instance's /api/tags.
func (c *Client) HasEmbeddingModel() bool {
	tags, err := c.tags()
	if err != nil {
		return false
	}
	want := EmbedModel()
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// EmbedText embeds a single text with the local Ollama embedding model
// (POST /api/embed). It returns a dense vector of float32s. Errors when Ollama
// is unreachable or the model is missing — callers keep their deterministic
// fallback in that case.
func (c *Client) EmbedText(ctx context.Context, text string) ([]float32, error) {
	model := EmbedModel()
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": text,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed status %d", resp.StatusCode)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) == 0 {
		return nil, errors.New("empty ollama embedding")
	}
	return out.Embeddings[0], nil
}

func (c *Client) tags() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, c.Base+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}
