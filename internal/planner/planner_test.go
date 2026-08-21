package planner

import (
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
)

// stubProvider implements agent.Provider for testing. It records the last
// prompt so tests can assert on what was sent.
type stubProvider struct {
	response string
	err      error
	lastPrompt string
	lastOpts   agent.Option
}

func (s *stubProvider) Generate(prompt string, options ...agent.Option) (string, error) {
	s.lastPrompt = prompt
	if len(options) > 0 {
		s.lastOpts = options[0]
	}
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

func TestPlanNoProvider(t *testing.T) {
	a := New(nil)
	if _, err := a.Plan("add a cache layer", nil); err != ErrNoProvider {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestPlanGeneratesPlan(t *testing.T) {
	a := New(&stubProvider{response: "  ## Objective\nDo the thing.  "})
	plan, err := a.Plan("add a cache layer", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan != "## Objective\nDo the thing." {
		t.Fatalf("expected trimmed plan, got %q", plan)
	}
}

func TestPlanWithMemories(t *testing.T) {
	s := &stubProvider{response: "plan"}
	a := New(s)
	if _, err := a.Plan("add cache", []string{"use redis", "avoid locks"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(s.lastPrompt, "use redis") {
		t.Fatalf("expected memory 'use redis' in prompt, got: %q", s.lastPrompt)
	}
	if !strings.Contains(s.lastPrompt, "avoid locks") {
		t.Fatalf("expected memory 'avoid locks' in prompt, got: %q", s.lastPrompt)
	}
}

func TestPlanProviderError(t *testing.T) {
	s := &stubProvider{err: errors.New("boom")}
	a := New(s)
	if _, err := a.Plan("add cache", nil); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped error to contain 'boom', got %v", err)
	}
}

func TestBuildUserPrompt(t *testing.T) {
	a := New(nil)
	p := a.buildPrompt("add a cache layer", []string{"m1", "m2"})
	if !strings.Contains(p, "add a cache layer") {
		t.Fatalf("expected intent in prompt, got: %q", p)
	}
	if !strings.Contains(p, "m1") || !strings.Contains(p, "m2") {
		t.Fatalf("expected memories in prompt, got: %q", p)
	}
	if !strings.Contains(p, "## Objective") {
		t.Fatalf("expected system prompt formatting in prompt, got: %q", p)
	}
}

// ensure stub satisfies agent.Provider at compile time.
var _ agent.Provider = (*stubProvider)(nil)