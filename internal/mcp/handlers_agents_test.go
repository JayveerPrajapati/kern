package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

func TestHandleLLMProviders(t *testing.T) {
	s := &Server{}
	out, err := s.handleLLMProviders(context.Background(), map[string]any{"probe": false})
	if err != nil {
		t.Fatalf("handleLLMProviders error: %v", err)
	}
	if !strings.Contains(out, "provider:") {
		t.Fatalf("output missing provider header: %s", out)
	}

	// Test with registered host sampler
	disposer := llm.RegisterHostSampler(func(ctx context.Context, system, user string, opts llm.Options) (string, error) {
		return "ok", nil
	})
	defer disposer()

	outHost, err := s.handleLLMProviders(context.Background(), map[string]any{"probe": false})
	if err != nil {
		t.Fatalf("handleLLMProviders with host sampler error: %v", err)
	}
	if !strings.Contains(outHost, "host") || !strings.Contains(outHost, "active MCP host sampling connected") {
		t.Fatalf("output should include active host sampler: %s", outHost)
	}
}
