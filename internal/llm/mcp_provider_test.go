package llm

import (
	"context"
	"fmt"
	"testing"
)

func TestMCPProviderDirectSampler(t *testing.T) {
	var gotSys, gotUser string
	var gotOpts Options
	mockSampler := func(ctx context.Context, system, user string, opts Options) (string, error) {
		gotSys = system
		gotUser = user
		gotOpts = opts
		return "sampled output", nil
	}

	prov := NewMCPProvider(mockSampler)
	out, err := prov.Generate(context.Background(), "system prompt", "user query", Options{
		Model:       "claude-3-7-sonnet",
		MaxTokens:   1500,
		Temperature: 0.2,
	})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if out != "sampled output" {
		t.Fatalf("Generate returned %q, want 'sampled output'", out)
	}
	if gotSys != "system prompt" || gotUser != "user query" {
		t.Fatalf("Sampler received sys=%q user=%q, want 'system prompt' and 'user query'", gotSys, gotUser)
	}
	if gotOpts.Model != "claude-3-7-sonnet" || gotOpts.MaxTokens != 1500 || gotOpts.Temperature != 0.2 {
		t.Fatalf("Sampler received opts=%+v, want Model=claude-3-7-sonnet MaxTokens=1500 Temperature=0.2", gotOpts)
	}

	cap := prov.Capabilities()
	if !cap.Generate || cap.Embed || cap.Stream {
		t.Fatalf("Capabilities = %+v, want Generate=true Embed=false Stream=false", cap)
	}

	if _, err := prov.Embed(context.Background(), "test"); err == nil {
		t.Fatal("Embed should return error on MCPProvider")
	}
	if _, err := prov.Stream(context.Background(), "sys", "user", Options{}); err == nil {
		t.Fatal("Stream should return error on MCPProvider")
	}
}

func TestMCPProviderGlobalSamplerRegistration(t *testing.T) {
	if HasHostSampler() {
		t.Fatal("HasHostSampler should be false initially")
	}

	// Generating without registered sampler should fail
	prov := NewMCPProvider()
	if _, err := prov.Generate(context.Background(), "sys", "user", Options{}); err == nil {
		t.Fatal("Generate should fail when no host sampler is registered")
	}

	// Register host sampler
	disposer := RegisterHostSampler(func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "host response: " + user, nil
	})
	defer disposer()

	if !HasHostSampler() {
		t.Fatal("HasHostSampler should be true after registration")
	}

	out, err := prov.Generate(context.Background(), "", "hello world", Options{})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if out != "host response: hello world" {
		t.Fatalf("Generate got %q, want 'host response: hello world'", out)
	}

	// Calling disposer unregisters sampler
	disposer()
	if HasHostSampler() {
		t.Fatal("HasHostSampler should be false after disposer runs")
	}

	if _, err := prov.Generate(context.Background(), "sys", "user", Options{}); err == nil {
		t.Fatal("Generate should fail after disposer runs")
	}
}

func TestNewProviderWithMCPConfig(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "host")
	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider(host) error: %v", err)
	}
	if _, ok := p.(*MCPProvider); !ok {
		t.Fatalf("Provider = %T, want *MCPProvider", p)
	}

	t.Setenv("KERN_LLM_PROVIDER", "mcp")
	p2, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider(mcp) error: %v", err)
	}
	if _, ok := p2.(*MCPProvider); !ok {
		t.Fatalf("Provider = %T, want *MCPProvider", p2)
	}
}

func TestNewProviderAutoChainIncludesHostSamplerWhenPresent(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "")
	disposer := RegisterHostSampler(func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "host-handled", nil
	})
	defer disposer()

	p, err := NewProvider()
	if err != nil {
		t.Fatalf("NewProvider error: %v", err)
	}

	chain, ok := p.(*ChainProvider)
	if !ok {
		t.Fatalf("p = %T, want *ChainProvider", p)
	}

	providers := chain.Providers()
	if len(providers) == 0 {
		t.Fatal("chain is empty")
	}
	if _, ok := providers[0].(*MCPProvider); !ok {
		t.Fatalf("first provider in chain = %T, want *MCPProvider when host sampler is registered", providers[0])
	}

	got, err := p.Generate(context.Background(), "", "test", Options{})
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if got != "host-handled" {
		t.Fatalf("got %q, want 'host-handled'", got)
	}
}

func TestMaskRequiredWithMCPProvider(t *testing.T) {
	t.Setenv("KERN_LLM_PROVIDER", "host")
	if MaskRequired() {
		t.Fatal("MaskRequired() = true for host provider, want false")
	}
}

// TestRegisterHostSamplerForMultipleKeys: several sessions/agents can each
// register their own sampler; the provider tries them in registration order
// (first success wins) and disposing one key leaves the others intact.
func TestRegisterHostSamplerForMultipleKeys(t *testing.T) {
	agentA := RegisterHostSamplerFor("agent-a", func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "A:" + user, nil
	})
	agentB := RegisterHostSamplerFor("agent-b", func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "", fmt.Errorf("agent-b down")
	})
	agentC := RegisterHostSamplerFor("agent-c", func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "C:" + user, nil
	})
	defer agentA()
	defer agentB()
	defer agentC()

	prov := NewMCPProvider()

	// First registered key answers.
	out, err := prov.Generate(context.Background(), "", "u", Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != "A:u" {
		t.Errorf("got %q, want A:u (first registered sampler wins)", out)
	}

	// Dispose A: B fails, so C answers.
	agentA()
	out, err = prov.Generate(context.Background(), "", "u", Options{})
	if err != nil {
		t.Fatalf("Generate after disposing A: %v", err)
	}
	if out != "C:u" {
		t.Errorf("got %q, want C:u (failing sampler skipped)", out)
	}

	// Replace B with a working sampler under the same key (re-registration
	// keeps the slot's original position — C was registered before B, so C
	// still wins; disposing C then lets the replaced B answer).
	agentB()
	agentB2 := RegisterHostSamplerFor("agent-b", func(ctx context.Context, system, user string, opts Options) (string, error) {
		return "B2:" + user, nil
	})
	defer agentB2()
	out, err = prov.Generate(context.Background(), "", "u", Options{})
	if err != nil {
		t.Fatalf("Generate after re-registering B: %v", err)
	}
	if out != "C:u" {
		t.Errorf("got %q, want C:u (earlier-registered sampler still first)", out)
	}
	agentC()
	out, err = prov.Generate(context.Background(), "", "u", Options{})
	if err != nil {
		t.Fatalf("Generate after disposing C: %v", err)
	}
	if out != "B2:u" {
		t.Errorf("got %q, want B2:u", out)
	}

	// Dispose everything: no sampler left.
	agentB2()
	agentC()
	if HasHostSampler() {
		t.Fatal("HasHostSampler should be false after disposing every key")
	}
	if _, err := prov.Generate(context.Background(), "", "u", Options{}); err == nil {
		t.Fatal("Generate should fail with no samplers registered")
	}
}
