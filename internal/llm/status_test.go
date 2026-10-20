package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withNoAgents pins PATH to an empty dir so no real agent CLI leaks into
// the classification (the dev machine has several installed).
func withNoAgents(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// withAgentStub puts a single executable agent stub on PATH.
func withAgentStub(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// unreachableOllama points the client at a loopback port nothing listens on
// (connection refused is instant, well under the 2s probe timeout).
func unreachableOllama(t *testing.T) {
	t.Helper()
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
}

func TestStatusDisabled(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "none")
	st := Status()
	if st.Level != "ok" || !strings.Contains(st.Detail, "disabled") {
		t.Errorf("got %q (%s), want ok/disabled", st.Detail, st.Level)
	}
}

func TestStatusPinnedOpenAIMissingKey(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "openai")
	st := Status()
	if st.Level != "warn" || !strings.Contains(st.Detail, "OPENAI_API_KEY") {
		t.Errorf("got %q (%s), want warn naming OPENAI_API_KEY", st.Detail, st.Level)
	}
}

func TestStatusPinnedOpenAIWired(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	st := Status()
	if st.Level != "ok" || !strings.Contains(st.Detail, "KERN_LLM_PROVIDER=openai") {
		t.Errorf("got %q (%s), want ok pinned-wiring detail", st.Detail, st.Level)
	}
}

func TestStatusPinnedGoogleMissingKey(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "google")
	st := Status()
	if st.Level != "warn" || !strings.Contains(st.Detail, "GEMINI_API_KEY") {
		t.Errorf("got %q (%s), want warn naming GEMINI_API_KEY", st.Detail, st.Level)
	}
}

func TestStatusAutoNoProvider(t *testing.T) {
	withNoAgents(t)
	unreachableOllama(t)
	st := Status()
	if st.Level != "warn" || !strings.Contains(st.Detail, "no agent CLI installed") {
		t.Errorf("got %q (%s), want warn naming the missing agent CLI", st.Detail, st.Level)
	}
}

func TestStatusAutoCLIsServeOllamaDown(t *testing.T) {
	withAgentStub(t, "claude")
	unreachableOllama(t)
	st := Status()
	if st.Level != "ok" {
		t.Fatalf("got level %q, want ok (CLIs are the normal chain head), detail %q", st.Level, st.Detail)
	}
	for _, want := range []string{"served by local agent CLI(s): claude", "semantic doc search", "keyword mode"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("detail %q missing %q", st.Detail, want)
		}
	}
}

func TestStatusPinnedOllamaHasNoCLIFallback(t *testing.T) {
	withAgentStub(t, "claude")
	unreachableOllama(t)
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	st := Status()
	if st.Level != "warn" || !strings.Contains(st.Detail, "no agent-CLI fallback") {
		t.Errorf("got %q (%s), want warn naming the pinned no-fallback state", st.Detail, st.Level)
	}
}

func TestStatusRequiredEscalatesToFail(t *testing.T) {
	withNoAgents(t)
	unreachableOllama(t)
	t.Setenv("KERN_LLM_REQUIRED", "1")
	st := Status()
	if st.Level != "fail" || !strings.Contains(st.Detail, "KERN_LLM_REQUIRED") {
		t.Errorf("got %q (%s), want fail naming KERN_LLM_REQUIRED", st.Detail, st.Level)
	}
}

func TestKnownProviderTable(t *testing.T) {
	valid := []string{"auto", "ollama", "none", "off", "disabled", "mcp", "host", "sampling",
		"openai", "openrouter", "groq", "litellm", "vllm", "azure", "anthropic",
		"google", "gemini", "claude", "codex", "gemini-cli", "qwen", "agy", "antigravity"}
	for _, v := range valid {
		if !KnownProvider(v) {
			t.Errorf("KnownProvider(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "bogus", "OLLAMA", "gpt-4"} {
		if KnownProvider(v) {
			t.Errorf("KnownProvider(%q) = true, want false", v)
		}
	}
}

func TestCredentialEnvTable(t *testing.T) {
	for name, want := range map[string]string{
		"openai":     "OPENAI_API_KEY",
		"openrouter": "OPENAI_API_KEY",
		"anthropic":  "ANTHROPIC_API_KEY",
		"google":     "GEMINI_API_KEY",
		"claude":     "",
		"auto":       "",
	} {
		if got := CredentialEnv(name); got != want {
			t.Errorf("CredentialEnv(%q) = %q, want %q", name, got, want)
		}
	}
}
