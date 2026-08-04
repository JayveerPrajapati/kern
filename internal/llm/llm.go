// Package llm implements an optional, opt-in compression step backed by a
// local Ollama server. It is never used implicitly: callers must request it
// with an explicit model name (CLI --llm or the KERN_MODEL env var). If
// Ollama is unreachable the caller falls back to the deterministic path.
package llm

import (
	"bytes"
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
