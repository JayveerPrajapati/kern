// Package agentctl owns agent control and coordination MCP tool bodies
// (kern_agent action=message|interrupt|fingerprint|coordination|rbac,
// kern_llm_providers) as plain functions.
package agentctl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/mcp/coord"
	"github.com/JayveerPrajapati/kern/internal/mcp/fingerprint"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/rbac"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

// Hooks provides platform, coordination and audit dependencies from the
// owning MCP server for the message/interrupt/fingerprint bodies.
type Hooks struct {
	PlatformFor  func(ctx context.Context, root string) (*app.Platform, error)
	CoordHandoff func(root string, t time.Time, from string, format string, send map[string]any) (string, error)
	AuditFilter  func(agentID string) []governance.AuditEntry
	AuditAll     func() []governance.AuditEntry
}

// Tool is the consolidated kern_agent dispatcher: the action argument
// selects the message/interrupt/fingerprint/coordination/rbac body.
func Tool(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		return "", fmt.Errorf("kern_agent: 'action' is required")
	}
	switch action {
	case "message":
		return AgentMessage(ctx, h, args)
	case "interrupt":
		return AgentInterrupt(ctx, h, args)
	case "fingerprint":
		return fingerprint.Analyze(ctx, fingerprint.Hooks{
			AuditFilter: h.AuditFilter,
			AuditAll:    h.AuditAll,
		}, args)
	case "coordination", "rbac":
		// The leaf packages read args["action"] for their own sub-action;
		// the top-level action key is consumed by this dispatcher. The leaf
		// action is passed through "inner_action" and falls back to the
		// leaf's default when absent (coordination=status, rbac=roles).
		cp := make(map[string]any, len(args)+1)
		for k, v := range args {
			cp[k] = v
		}
		if inner := mcpargs.ArgString(args, "inner_action"); inner != "" {
			cp["action"] = inner
		} else {
			delete(cp, "action")
		}
		if action == "coordination" {
			return coord.Handle(ctx, cp)
		}
		return rbac.Handle(ctx, cp)
	default:
		return "", fmt.Errorf("kern_agent: unknown action %q (want message|interrupt|fingerprint|coordination|rbac)", action)
	}
}

// AgentMessage implements kern_agent action=message: the model sends a message to an agent's coordination inbox.
func AgentMessage(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	to := mcpargs.ArgString(args, "to_agent")
	if to == "" {
		return "", fmt.Errorf("to_agent is required")
	}
	notes := mcpargs.ArgString(args, "notes")
	if notes == "" {
		return "", fmt.Errorf("notes (the message) is required")
	}
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	from := mcpargs.ArgString(args, "from_agent")
	if from == "" {
		from = "model"
	}
	taskID := mcpargs.ArgString(args, "task_id")
	if taskID != "" && h.PlatformFor != nil {
		p, err := h.PlatformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := tasklife.NewTaskService(p, nil)
		if _, ok := ts.Get(taskID); !ok {
			return "", fmt.Errorf("task %q not found", taskID)
		}
	}

	send := map[string]any{
		"from_agent": from,
		"to_agent":   to,
		"notes":      notes,
	}
	if taskID != "" {
		send["task_id"] = taskID
	}
	if h.CoordHandoff == nil {
		return "", fmt.Errorf("coordination handoff hook not configured")
	}
	return h.CoordHandoff(root, time.Now().UTC(), from, "json", send)
}

// AgentInterrupt implements kern_agent action=interrupt: cancels a running task by ID.
func AgentInterrupt(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	taskID := mcpargs.ArgString(args, "task_id")
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	reason := mcpargs.ArgString(args, "reason")
	if reason == "" {
		reason = "interrupted by model via kern_agent"
	}
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))

	if h.PlatformFor == nil {
		return "", fmt.Errorf("platform hook not configured")
	}
	p, err := h.PlatformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := tasklife.NewTaskService(p, nil)
	if err := ts.Cancel(taskID, reason); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(map[string]any{
		"status":  "cancelled",
		"task_id": taskID,
		"reason":  reason,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// LLMProviders implements kern_llm_providers: reports active local providers and priority chain.
func LLMProviders(ctx context.Context, args map[string]any) (string, error) {
	probe, _ := args["probe"].(bool)
	var b strings.Builder
	chainNames := []string{}
	if llm.HasHostSampler() {
		chainNames = append(chainNames, "host")
	}
	chainNames = append(chainNames, llm.AvailableLocalAgents()...)
	chainNames = append(chainNames, "ollama")
	fmt.Fprintf(&b, "provider: %s (auto chain: %s)\n", llm.ProviderName(), strings.Join(chainNames, " → "))

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
				// The MCP ack: kern knows the active session(s) and the model
				// each serves — the SAME session does the task, no new session.
				note := "active MCP host sampling connected"
				if st := llm.HostSamplerStates(); len(st) > 0 {
					parts := make([]string, 0, len(st))
					for _, s := range st {
						if s.Model != "" {
							parts = append(parts, fmt.Sprintf("%s (%s)", s.Key, s.Model))
						} else {
							parts = append(parts, s.Key)
						}
					}
					note += ": " + strings.Join(parts, ", ")
				}
				write(name, "llm-provider", "ok", note)
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
