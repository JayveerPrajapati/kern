package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewProviderDefaultOllama(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != false {
			t.Fatalf("stream = %v, want false", body["stream"])
		}
		w.Write([]byte(`{"response":"ollama ok"}`))
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, ok := p.(*OllamaProvider); !ok {
		t.Fatalf("default provider = %T, want *OllamaProvider", p)
	}
	got, err := p.Generate(context.Background(), "sys", "user payload", Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "ollama ok" {
		t.Fatalf("got %q, want ollama ok", got)
	}
	if gotPath != "/api/generate" {
		t.Fatalf("path = %q, want /api/generate", gotPath)
	}
}

func TestNewProviderOpenAIWireAndOptions(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "openai")
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"openai ok"}}]}`))
	}))
	defer srv.Close()
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", "test-key")

	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, ok := p.(*OpenAICompatibleProvider); !ok {
		t.Fatalf("provider = %T, want *OpenAICompatibleProvider", p)
	}
	got, err := p.Generate(context.Background(), "system", "user msg", Options{
		Model:       "my-model",
		MaxTokens:   2000,
		Temperature: 0.3,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "openai ok" {
		t.Fatalf("got %q, want openai ok", got)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotBody["model"] != "my-model" {
		t.Fatalf("model = %v, want my-model", gotBody["model"])
	}
	if gotBody["max_tokens"] != float64(2000) {
		t.Fatalf("max_tokens = %v, want 2000", gotBody["max_tokens"])
	}
	if gotBody["temperature"] != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", gotBody["temperature"])
	}
}

func TestNewProviderAnthropicWire(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "anthropic")
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("x-api-key") != "k" {
			t.Fatalf("x-api-key = %q, want k", r.Header.Get("x-api-key"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"content":[{"type":"text","text":"claude ok"}]}`))
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_API_KEY", "k")

	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ap, ok := p.(*AnthropicProvider)
	if !ok {
		t.Fatalf("provider = %T, want *AnthropicProvider", p)
	}
	ap.BaseURL = srv.URL

	got, err := p.Generate(context.Background(), "sys", "user", Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "claude ok" {
		t.Fatalf("got %q, want claude ok", got)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotBody["max_tokens"] != float64(1024) {
		t.Fatalf("max_tokens = %v, want default 1024", gotBody["max_tokens"])
	}
	if gotBody["system"] != "sys" {
		t.Fatalf("system = %v, want sys", gotBody["system"])
	}
}

func TestNewProviderAnthropicMissingKey(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := NewProvider(); err == nil {
		t.Fatal("expected error when ANTHROPIC_API_KEY missing")
	}
}

func TestNewProviderGoogle(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "google")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"gemini ok"}]}}]}`))
	}))
	defer srv.Close()
	t.Setenv("GEMINI_API_KEY", "k")

	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	gp, ok := p.(*GoogleProvider)
	if !ok {
		t.Fatalf("provider = %T, want *GoogleProvider", p)
	}
	gp.BaseURL = srv.URL

	got, err := p.Generate(context.Background(), "sys", "user", Options{Model: "gemini-2.0"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "gemini ok" {
		t.Fatalf("got %q, want gemini ok", got)
	}
	if !strings.HasPrefix(gotPath, "/v1beta/models/gemini-2.0:generateContent") {
		t.Fatalf("path = %q, want model-specific generateContent", gotPath)
	}
}

func TestCapabilitiesDiffer(t *testing.T) {
	// Ollama + OpenAI embed; Anthropic + Google do not.
	if !NewOllamaProvider().Capabilities().Embed {
		t.Error("ollama should embed")
	}
	oc, err := NewOpenAIProvider()
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	if !oc.Capabilities().Embed {
		t.Error("openai should embed")
	}
	ap, _ := NewAnthropicProviderForTest()
	if ap.Capabilities().Embed {
		t.Error("anthropic should not embed")
	}
	if ap.Capabilities().Stream {
		t.Error("anthropic should not stream")
	}
	gp, _ := NewGoogleProviderForTest()
	if gp.Capabilities().Embed {
		t.Error("google should not embed")
	}
}

func TestOllamaStreamReturnsTokens(t *testing.T) {
	// /api/generate with stream=true returns NDJSON response chunks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("path = %q, want /api/generate", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Fatalf("stream = %v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"response":"hello "}` + "\n" + `{"response":"world"}` + "\n"))
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	p := NewOllamaProvider()
	st, err := p.Stream(context.Background(), "sys", "user", Options{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer st.Close()
	b, err := io.ReadAll(st.Reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(b); got != "hello world" {
		t.Fatalf("stream = %q, want \"hello world\"", got)
	}
}

func TestOpenAIStreamSSE(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "openai")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi \"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"there\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", "k")

	p, _ := NewProvider()
	oc, _ := p.(*OpenAICompatibleProvider)
	st, err := oc.Stream(context.Background(), "sys", "user", Options{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer st.Close()
	b, err := io.ReadAll(st.Reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(b); got != "hi there" {
		t.Fatalf("stream = %q, want \"hi there\"", got)
	}
}

func TestOpenAIEmbedWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %q, want /embeddings", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "text-embedding-3-small" {
			t.Fatalf("embed model = %v, want default", body["model"])
		}
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	oc, err := NewOpenAIProvider()
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	oc.BaseURL = srv.URL
	vec, err := oc.Embed(context.Background(), "some text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[2] != 0.3 {
		t.Fatalf("vec = %v, want [0.1 0.2 0.3]", vec)
	}
}

func TestNewEmbedderOllamaWire(t *testing.T) {
	// NewEmbedder selects the default (Ollama) embedder and embeds via /api/embed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path = %q, want /api/embed", r.URL.Path)
		}
		w.Write([]byte(`{"embeddings":[[0.5,0.5]]}`))
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("KERN_LLM_PROVIDER", "")

	e := NewEmbedder()
	vec, err := e.EmbedText("hello")
	if err != nil {
		t.Fatalf("EmbedText: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.5 {
		t.Fatalf("vec = %v, want [0.5 0.5]", vec)
	}
}

// NewAnthropicProviderForTest builds a provider without requiring env, for
// capability assertions only.
func NewAnthropicProviderForTest() (*AnthropicProvider, error) {
	return &AnthropicProvider{APIKey: "k", Model: "claude", BaseURL: "https://api.anthropic.com", HTTP: http.DefaultClient}, nil
}

// NewGoogleProviderForTest builds a provider without requiring env keys.
func NewGoogleProviderForTest() (*GoogleProvider, error) {
	return &GoogleProvider{APIKey: "k", Model: "gemini", BaseURL: "https://generativelanguage.googleapis.com", HTTP: http.DefaultClient}, nil
}
