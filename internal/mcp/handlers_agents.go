package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

// handleLLMProviders implements kern_llm_providers: which agents kern is attached to
// priority chain over locally-wired agent CLIs — the
// priority answer for "which local agent should I use" when Ollama is
// absent. Without a probe flag it reports binary presence; with probe=1 it
// live-tests each installed provider with a trivial prompt.
func (s *Server) handleLLMProviders(ctx context.Context, args map[string]any) (string, error) {
	probe, _ := args["probe"].(bool)
	var b strings.Builder
	chainNames := []string{}
	if llm.HasHostSampler() {
		chainNames = append(chainNames, "host")
	}
	chainNames = append(chainNames, "ollama")
	chainNames = append(chainNames, llm.AvailableLocalAgents()...)
	fmt.Fprintf(&b, "provider: %s (auto chain: %s)\n", llm.ProviderName(), strings.Join(chainNames, " → "))

	// LLM providers first (the priority question), then wired agents.
	write := func(name, kind, status, note string) {
		if note != "" {
			status += " — " + note
		}
		fmt.Fprintf(&b, "%-12s %-13s %s\n", name, kind, status)
	}
	for _, name := range chainNames {
		switch name {
		case "host":
			if llm.HasHostSampler() {
				write(name, "llm-provider", "ok", "active MCP host sampling connected")
			} else {
				write(name, "llm-provider", "unreachable", "no MCP host sampling registered")
			}
		case "ollama":
			c := llm.New("")
			if c.Available() {
				write(name, "llm-provider", "ok", c.Base+" reachable, model "+c.Model)
			} else {
				write(name, "llm-provider", "unreachable", c.Base+" not reachable")
			}
		default:
			p := llm.NewLocalCliProvider(name)
			if !p.Installed() {
				write(name, "llm-provider", "uninstalled", "")
				continue
			}
			if !probe {
				write(name, "llm-provider", "installed (unprobed)", "run with probe=true to live-test")
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, 120e9) // 120s
			out, err := p.Generate(pctx, "", "Reply with exactly: OK", llm.Options{})
			cancel()
			if err == nil && strings.TrimSpace(out) != "" {
				write(name, "llm-provider", "ok", "")
			} else {
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				write(name, "llm-provider", "error", msg)
			}
		}
	}
	return b.String(), nil
}
