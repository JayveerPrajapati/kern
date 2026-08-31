package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- SSRF guard: ValidateBaseURL -----------------------------------------

func TestValidateBaseURL_RejectsPrivate(t *testing.T) {
	// Private RFC1918 ranges and link-local targets (incl. the cloud metadata
	// endpoint) must be rejected regardless of scheme. Loopback IPv6 (::1) is
	// also rejected because the local exception only covers "localhost" and
	// "127.0.0.1".
	bad := []string{
		"https://10.0.0.5",
		"http://10.0.0.5",
		"https://172.16.0.1",
		"http://172.16.0.1",
		"https://192.168.1.1",
		"http://192.168.1.1",
		"https://169.254.169.254", // link-local: AWS instance metadata
		"http://169.254.169.254",
		"https://[::1]",
	}
	for _, u := range bad {
		if err := ValidateBaseURL(u); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want error", u)
		}
	}
}

func TestValidateBaseURL_RejectsHTTP(t *testing.T) {
	// http is only allowed for localhost/127.0.0.1 — any remote host over
	// cleartext http must be rejected (the Bearer key must never go out
	// unencrypted).
	for _, u := range []string{
		"http://api.openai.com",
		"http://api.openai.com/v1",
		"http://example.com",
		"http://internal.corp",
	} {
		if err := ValidateBaseURL(u); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want error (https required)", u)
		}
	}
}

func TestValidateBaseURL_AcceptsHTTPS(t *testing.T) {
	for _, u := range []string{
		"https://api.openai.com",
		"https://api.openai.com/v1",
		"https://example.com/v1",
	} {
		if err := ValidateBaseURL(u); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateBaseURL_AcceptsLocalHTTP(t *testing.T) {
	// Documented local exception: http is allowed when the host is
	// localhost/127.0.0.1 (local testing).
	for _, u := range []string{
		"http://localhost:11434",
		"http://localhost",
		"http://127.0.0.1:11434",
		"https://localhost:8443",
		"https://127.0.0.1",
	} {
		if err := ValidateBaseURL(u); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateBaseURL_InvalidURL(t *testing.T) {
	for _, u := range []string{"://bad", "%"} {
		if err := ValidateBaseURL(u); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want error", u)
		}
	}
}

func TestNewOpenAIProvider_RejectsUnsafeBaseURL(t *testing.T) {
	for _, base := range []string{
		"http://api.openai.com",
		"https://169.254.169.254",
		"http://10.0.0.5",
		"https://192.168.1.1",
	} {
		t.Setenv("OPENAI_BASE_URL", base)
		if _, err := NewOpenAIProvider(); err == nil {
			t.Errorf("NewOpenAIProvider with OPENAI_BASE_URL=%q: want error", base)
		}
	}
}

func TestNewOpenAIProvider_AcceptsHTTPSBaseURL(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	p, err := NewOpenAIProvider()
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q, want https://api.openai.com/v1", p.BaseURL)
	}
}

// --- isLocalHost ----------------------------------------------------------

func TestIsLocalHost(t *testing.T) {
	local := []string{
		"localhost",
		"http://localhost:11434",
		"127.0.0.1",
		"http://127.0.0.1:11434",
		"::1",
		"http://[::1]:11434",
		"0.0.0.0",
	}
	for _, u := range local {
		if !isLocalHost(u) {
			t.Errorf("isLocalHost(%q) = false, want true", u)
		}
	}
	remote := []string{
		"10.0.0.5",
		"http://example.com",
		"http://192.168.1.1:8080",
		"localhost.evil.com",
		"http://169.254.169.254",
	}
	for _, u := range remote {
		if isLocalHost(u) {
			t.Errorf("isLocalHost(%q) = true, want false", u)
		}
	}
}

// --- MaskRequired ---------------------------------------------------------

func TestMaskRequired_LocalOllama(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://localhost:11434")
	if MaskRequired() {
		t.Error("MaskRequired() = true for local ollama, want false")
	}
}

func TestMaskRequired_RemoteOpenAI(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "openai")
	if !MaskRequired() {
		t.Error("MaskRequired() = false for remote openai, want true")
	}
}

func TestMaskRequired_RemoteOllamaHost(t *testing.T) {
	// Ollama pointed at a LAN/remote host must be treated as remote too.
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "http://10.0.0.5:11434")
	if !MaskRequired() {
		t.Error("MaskRequired() = false for ollama on a LAN host, want true")
	}
}

// --- Client embedding endpoints -------------------------------------------

func TestClientEmbedText(t *testing.T) {
	t.Setenv("KERN_EMBED_MODEL", "my-embed")
	srv := mockOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path = %q, want /api/embed", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("bad body: %v", err)
		}
		if body["model"] != "my-embed" {
			t.Errorf("model = %v, want my-embed", body["model"])
		}
		if body["input"] != "hello world" {
			t.Errorf("input = %v, want hello world", body["input"])
		}
		w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	})
	defer srv.Close()

	c := New("test-model")
	c.Base = srv.URL
	vec, err := c.EmbedText(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("EmbedText: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[2] != 0.3 {
		t.Fatalf("vec = %v, want [0.1 0.2 0.3]", vec)
	}
}

func TestClientEmbedText_Errors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := mockOllama(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		})
		defer srv.Close()
		c := New("")
		c.Base = srv.URL
		if _, err := c.EmbedText(context.Background(), "x"); err == nil {
			t.Error("expected error on non-200")
		}
	})
	t.Run("empty-embeddings", func(t *testing.T) {
		srv := mockOllama(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"embeddings":[]}`))
		})
		defer srv.Close()
		c := New("")
		c.Base = srv.URL
		_, err := c.EmbedText(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "empty ollama embedding") {
			t.Errorf("got %v, want empty-embedding error", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		c := New("")
		c.Base = "http://127.0.0.1:1"
		if _, err := c.EmbedText(context.Background(), "x"); err == nil {
			t.Error("expected error when unreachable")
		}
	})
}

func TestClientHasEmbeddingModel(t *testing.T) {
	t.Setenv("KERN_EMBED_MODEL", "my-embed")
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"present", `{"models":[{"name":"my-embed"},{"name":"llama3.2"}]}`, true},
		{"case-insensitive", `{"models":[{"name":"MY-EMBED"}]}`, true},
		{"missing", `{"models":[{"name":"other-model"}]}`, false},
		{"empty", `{"models":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/tags" {
					t.Fatalf("path = %q, want /api/tags", r.URL.Path)
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New("")
			c.Base = srv.URL
			if got := c.HasEmbeddingModel(); got != tc.want {
				t.Errorf("HasEmbeddingModel() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClientHasEmbeddingModel_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New("")
	c.Base = srv.URL
	if c.HasEmbeddingModel() {
		t.Error("HasEmbeddingModel() = true on server error, want false")
	}
}

// --- CompressVia (provider-agnostic entry) --------------------------------

type fakeProvider struct {
	generate func(ctx context.Context, system, user string, opts Options) (string, error)
}

func (f fakeProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	return f.generate(ctx, system, user, opts)
}
func (f fakeProvider) Embed(ctx context.Context, text string) ([]float32, error) { return nil, nil }
func (f fakeProvider) Capabilities() Capabilities                                { return Capabilities{Generate: true} }
func (f fakeProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	return nil, nil
}

func TestCompressVia_NilProvider(t *testing.T) {
	if _, err := CompressVia(context.Background(), nil, "x", Options{}); err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestCompressVia_UsesCompressInstruction(t *testing.T) {
	fp := fakeProvider{generate: func(_ context.Context, system, user string, _ Options) (string, error) {
		if system != CompressInstruction {
			t.Errorf("system = %q, want CompressInstruction", system)
		}
		if user != "the prompt" {
			t.Errorf("user = %q, want the prompt", user)
		}
		return "compressed", nil
	}}
	got, err := CompressVia(context.Background(), fp, "the prompt", Options{})
	if err != nil {
		t.Fatalf("CompressVia: %v", err)
	}
	if got != "compressed" {
		t.Errorf("got %q, want compressed", got)
	}
}
