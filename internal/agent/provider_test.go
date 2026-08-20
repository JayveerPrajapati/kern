package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyOptions(t *testing.T) {
	req := ApplyOptions([]Option{
		WithModel("llama3.2"),
		WithMaxTokens(2048),
		WithTemperature(0.7),
	})
	if req.Model != "llama3.2" {
		t.Fatalf("Model = %q, want llama3.2", req.Model)
	}
	if req.MaxTokens != 2048 {
		t.Fatalf("MaxTokens = %d, want 2048", req.MaxTokens)
	}
	if req.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", req.Temperature)
	}
}

func TestApplyOptionsNoOptions(t *testing.T) {
	req := ApplyOptions(nil)
	if req.Model != "" || req.MaxTokens != 0 || req.Temperature != 0 {
		t.Fatalf("default Request = %+v, want zero value", req)
	}
}

func TestApplyOptionsOverrides(t *testing.T) {
	// Later options win (options are applied in order).
	req := ApplyOptions([]Option{WithModel("a"), WithModel("b")})
	if req.Model != "b" {
		t.Fatalf("Model = %q, want b (last option wins)", req.Model)
	}
}

func TestOllamaProviderNonNil(t *testing.T) {
	p := OllamaProvider()
	if p == nil {
		t.Fatal("OllamaProvider() returned nil")
	}
	// It must satisfy the Provider interface.
	var _ Provider = p
}

func TestProviderHonorsOptions(t *testing.T) {
	// The agent Provider routes through the provider-neutral factory. With
	// KERN_LLM_PROVIDER=openai, the issued request must carry the model
	// override, temperature and max_tokens from the options.
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	t.Setenv("KERN_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", "k")

	p := OllamaProvider()
	got, err := p.Generate("fix this build", WithModel("my-model"), WithTemperature(0.4), WithMaxTokens(512))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "ok" {
		t.Fatalf("got %q, want ok", got)
	}
	if gotBody["model"] != "my-model" {
		t.Fatalf("model = %v, want my-model", gotBody["model"])
	}
	if gotBody["temperature"] != 0.4 {
		t.Fatalf("temperature = %v, want 0.4", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens = %v, want 512", gotBody["max_tokens"])
	}
}
