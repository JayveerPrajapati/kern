package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// agentReport is one entry in the agents status: an agent kern is wired
// into (setup), an LLM-provider CLI, or the Ollama server.
type agentReport struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "wired-agent" | "llm-provider"
	Installed bool   `json:"installed"`
	Healthy   string `json:"healthy,omitempty"` // "ok" | "error" | "unprobed"
	Note      string `json:"note,omitempty"`
	Latency   string `json:"latency_ms,omitempty"`
}

// probeTimeout caps each --probe live-test so `kern agents --probe` cannot
// hang: a dead Ollama fails in milliseconds, a hung agent CLI is killed
// (whole process group) at the deadline, and the full probe finishes in
// bounded time.
const probeTimeout = 20 * time.Second

// runAgents implements `kern agents`: which agents kern is attached to
// (setup wiring), which of them can serve as LLM providers (CLI presence),
// and — with --probe — which one actually answers right now (the priority
// pick for LLM-dependent features when Ollama is absent).
func runAgents(rest []string) {
	// --probe is not a parseFlags option; strip it before flag parsing.
	probe := false
	clean := make([]string, 0, len(rest))
	for _, a := range rest {
		if a == "--probe" {
			probe = true
			continue
		}
		clean = append(clean, a)
	}
	f, args, err := parseFlags(clean)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	var reports []agentReport

	// 1. Wired agents (kern setup state) — the attachment history.
	for _, s := range setup.Check(root) {
		lvl := "ok"
		if !s.Installed {
			lvl = "uninstalled"
		}
		reports = append(reports, agentReport{
			Name:      s.Agent,
			Kind:      "wired-agent",
			Installed: s.Installed,
			Healthy:   lvl,
			Note:      s.Note,
		})
	}

	// 2. LLM provider chain: Host (if connected) + Ollama + agent CLIs, in auto priority order.
	provider := llm.ProviderName()
	var chainNames []string
	if llm.HasHostSampler() {
		chainNames = append(chainNames, "host")
	}
	chainNames = append(chainNames, "ollama")
	chainNames = append(chainNames, llm.AvailableLocalAgents()...)
	for _, name := range chainNames {
		r := agentReport{Name: name, Kind: "llm-provider"}
		switch name {
		case "host":
			if llm.HasHostSampler() {
				r.Installed = true
				r.Healthy = "ok"
				r.Note = "active MCP host sampling connected"
			} else {
				r.Installed = false
				r.Healthy = "unreachable"
				r.Note = "no MCP host sampling registered"
			}
		case "ollama":
			c := llm.New("")
			if c.Available() {
				r.Installed = true
				r.Healthy = "ok"
				r.Note = c.Base + " reachable, model " + c.Model
			} else {
				r.Installed = false
				r.Healthy = "unreachable"
				r.Note = c.Base + " not reachable"
			}
		default:
			p := llm.NewLocalCliProvider(name)
			if p.Installed() {
				r.Installed = true
				r.Healthy = "unprobed"
			} else {
				r.Installed = false
				r.Healthy = "uninstalled"
			}
		}
		reports = append(reports, r)
	}

	// 3. Live probe (--probe): ask each installed provider a trivial
	// question and report who actually answers — the real priority order.
	// Every probe is bounded by probeTimeout and announces itself on stderr
	// so a slow probe is visible instead of a silent hang.
	if probe {
		for i := range reports {
			r := &reports[i]
			if r.Kind != "llm-provider" || !r.Installed {
				continue
			}
			fmt.Fprintf(os.Stderr, "kern: probing %s ...\n", r.Name)
			start := time.Now()
			var out string
			var perr error
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			switch r.Name {
			case "host":
				prov := llm.NewMCPProvider()
				out, perr = prov.Generate(ctx, "", "Reply with exactly: OK", llm.Options{})
			case "ollama":
				// Probe Ollama directly, not the whole auto chain: a chain
				// probe would burn the budget spawning agent CLIs after the
				// (already unreachable, failing-in-ms) Ollama leg.
				prov := llm.NewOllamaProvider()
				out, perr = prov.Generate(ctx, "", "Reply with exactly: OK", llm.Options{})
			default:
				prov := llm.NewLocalCliProvider(r.Name)
				out, perr = prov.Generate(ctx, "", "Reply with exactly: OK", llm.Options{})
			}
			cancel()
			r.Latency = fmt.Sprintf("%d", time.Since(start).Milliseconds())
			if perr == nil && strings.TrimSpace(out) != "" {
				r.Healthy = "ok"
			} else {
				r.Healthy = "error"
				if perr != nil {
					r.Note = perr.Error()
				}
			}
		}
		// Priority hint: first working provider.
		for _, r := range reports {
			if r.Kind == "llm-provider" && r.Healthy == "ok" {
				fmt.Fprintf(os.Stderr, "kern: priority LLM provider: %s (%sms)\n", r.Name, r.Latency)
				break
			}
		}
	}

	if f.json {
		printJSON(reports)
		return
	}
	fmt.Printf("provider: %s (auto chain: %s)\n", provider, strings.Join(chainNames, " → "))
	for _, r := range reports {
		status := r.Healthy
		if status == "" {
			status = "?"
		}
		extra := r.Note
		if r.Latency != "" {
			extra = strings.TrimSpace(extra + " [" + r.Latency + "ms]")
		}
		line := fmt.Sprintf("%-14s %-13s %-12s", r.Name, r.Kind, status)
		if extra != "" {
			line += " " + extra
		}
		fmt.Println(line)
	}
}
