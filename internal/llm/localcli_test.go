package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgentCLI writes a fake agent binary into a temp dir and prepends it to
// PATH. The fake echoes the prompt back wrapped in markers so tests can assert
// the full argv/stdin plumbing. Returns the bin dir and a cleanup func.
func fakeAgentCLI(t *testing.T, name string, behavior string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	switch behavior {
	case "echo":
		// echo the LAST argument (the prompt) regardless of flag count
		script += "for last; do :; done; echo \"[[$last]]\"\n"
	case "fail":
		script += "echo 'boom' >&2\nexit 1\n"
	case "empty":
		script += "exit 0\n"
	case "slow":
		script += "sleep 5\n"
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolatePATH restricts PATH to the given fake dirs (host agent CLIs like a
// real claude/codex would leak into availability probes otherwise; the fake
// scripts use absolute /bin/sh so a restricted PATH is safe).
func isolatePATH(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func TestLocalCliProviderGenerate(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("claude")
	got, err := p.Generate(context.Background(), "sys", "user text", Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "[[sys\n\nuser text]]" {
		t.Fatalf("got %q, want system+user joined as the single prompt arg", got)
	}
	if cap := p.Capabilities(); !cap.Generate || cap.Embed || cap.Stream {
		t.Fatalf("capabilities = %+v, want Generate-only", cap)
	}
}

func TestLocalCliProviderMissingBinary(t *testing.T) {
	// No fake on PATH for this agent name: it must fail loudly and
	// actionably, not hang or silently no-op.
	p := NewLocalCliProvider("gemini")
	_, err := p.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error should name the missing install: %v", err)
	}
}

func TestLocalCliProviderFailureSurfacesStderr(t *testing.T) {
	d1 := fakeAgentCLI(t, "codex", "fail")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("codex")
	_, err := p.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not surfaced in error: %v", err)
	}
}

func TestLocalCliProviderEmptyOutput(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "empty")
	isolatePATH(t, d1)
	p := NewLocalCliProvider("claude")
	if _, err := p.Generate(context.Background(), "s", "u", Options{}); err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestAvailableLocalAgents(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	d2 := fakeAgentCLI(t, "qwen", "echo")
	isolatePATH(t, d1, d2)
	got := AvailableLocalAgents()
	if len(got) != 2 || got[0] != "claude" || got[1] != "qwen" {
		t.Fatalf("AvailableLocalAgents = %v, want [claude qwen] in preference order", got)
	}
}

func TestChainProviderFallsThrough(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	d2 := fakeAgentCLI(t, "codex", "echo")
	isolatePATH(t, d1, d2)
	chain := NewChainProvider(
		NewLocalCliProvider("gemini"), // missing binary -> fails
		NewLocalCliProvider("claude"), // available -> succeeds
	)
	got, err := chain.Generate(context.Background(), "s", "u", Options{})
	if err != nil {
		t.Fatalf("chain should fall through to claude: %v", err)
	}
	if got != "[[s\n\nu]]" {
		t.Fatalf("chain result = %q, want the claude fake output (system+user)", got)
	}
	if !chain.AllLocal() {
		t.Fatal("chain of local providers must be AllLocal")
	}
}

func TestChainProviderAllFailNamesProviders(t *testing.T) {
	d1 := fakeAgentCLI(t, "codex", "fail")
	isolatePATH(t, d1)
	chain := NewChainProvider(
		NewLocalCliProvider("gemini"), // missing
		NewLocalCliProvider("codex"),  // fails with stderr
	)
	_, err := chain.Generate(context.Background(), "s", "u", Options{})
	if err == nil {
		t.Fatal("expected all-fail error")
	}
	if !strings.Contains(err.Error(), "gemini") || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error must name every tried provider: %v", err)
	}
}

func TestChainProviderEmbedFallsToCapableOnly(t *testing.T) {
	d1 := fakeAgentCLI(t, "claude", "echo")
	isolatePATH(t, d1)
	chain := NewChainProvider(NewLocalCliProvider("claude"))
	if _, err := chain.Embed(context.Background(), "x"); err == nil {
		t.Fatal("embed must fail cleanly when no provider supports it")
	}
}
