// Package planner drives an LLM to generate an implementation plan for a given
// intent. It is the plan-stage equivalent of internal/coder: where the coder
// generates and verifies a patch, the planner generates a structured plan that
// the coder (or a human) can follow.
//
// The planner is provider-neutral: it talks only to the agent.Provider
// interface. A nil provider makes Plan return ErrNoProvider so callers can fall
// back to a deterministic plan or skip the stage.
package planner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/pii"
)

// ErrNoProvider is returned when the planner has no LLM provider configured.
// Callers should treat this as "planner unavailable" and fall back to an empty
// plan or skip the plan stage.
var ErrNoProvider = errors.New("planner: no LLM provider configured")

// Agent drives an LLM to generate an implementation plan.
type Agent struct {
	provider  agent.Provider // LLM provider (nil = unavailable)
	model     string         // model name override (empty = provider default)
	maxTokens int            // token cap per generation
}

// Option configures an Agent.
type Option func(*Agent)

// WithModel sets the LLM model name.
func WithModel(m string) Option {
	return func(a *Agent) { a.model = m }
}

// WithMaxTokens sets the token cap per generation.
func WithMaxTokens(n int) Option {
	return func(a *Agent) { a.maxTokens = n }
}

// New creates a planner Agent. A nil provider makes Plan return ErrNoProvider
// instead of silently no-op'ing.
func New(provider agent.Provider, opts ...Option) *Agent {
	a := &Agent{provider: provider}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// planSystemPrompt is the system prompt for plan generation. It is prepended to
// the user prompt so the single agent.Provider.Generate call receives both.
const planSystemPrompt = `You are a software engineering planner. Given a change intent and optional context, produce a concise implementation plan.

Format your plan as:
## Objective
<one-sentence objective>

## Risk
<low|medium|high>: <brief justification>

## Scope
<comma-separated list of files/packages affected>

## Implementation Steps
1. <step>
2. <step>
...

## Rollback
<how to undo this change>

Keep the plan focused and actionable. Do not write code — only the plan.

`

// Plan generates an implementation plan for the given intent, optionally
// informed by recalled memories (which provide prior context/lessons).
// Returns the plan text and an error. ErrNoProvider is returned when no LLM
// provider is configured.
func (a *Agent) Plan(intent string, memories []string) (string, error) {
	if a.provider == nil {
		return "", ErrNoProvider
	}

	prompt := a.buildPrompt(intent, memories)

	// PII-mask when the provider sends the prompt off the local machine, so
	// intent and memory contents never leak to a remote LLM unmasked (same
	// guard as coder). Local Ollama is left untouched since nothing leaves the
	// box.
	if llm.MaskRequired() {
		prompt = pii.MaskNames(prompt, nil).Text
	}

	var genOpts []agent.Option
	if a.model != "" {
		genOpts = append(genOpts, agent.WithModel(a.model))
	}
	if a.maxTokens > 0 {
		genOpts = append(genOpts, agent.WithMaxTokens(a.maxTokens))
	}

	plan, err := a.provider.Generate(prompt, genOpts...)
	if err != nil {
		return "", fmt.Errorf("planner: generate: %w", err)
	}
	return strings.TrimSpace(plan), nil
}

// buildPrompt assembles the plan-generation prompt from the system instruction,
// the intent, and any recalled memories.
func (a *Agent) buildPrompt(intent string, memories []string) string {
	var b strings.Builder
	b.WriteString(planSystemPrompt)
	fmt.Fprintf(&b, "Change intent: %s\n\n", intent)
	if len(memories) > 0 {
		b.WriteString("Relevant memories from prior work:\n")
		for i, m := range memories {
			fmt.Fprintf(&b, "%d. %s\n", i+1, m)
		}
		b.WriteString("\n")
	}
	b.WriteString("Produce an implementation plan for this change.")
	return b.String()
}