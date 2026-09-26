// Package runtime owns production-intelligence runtime MCP tool bodies
// (kern_runtime) as plain functions.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// Hooks provides index loading from the owning MCP server.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

func jsonOf(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Runtime exposes the production-intelligence layer over MCP.
func Runtime(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "status"
	}
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	var ix *index.Index
	var err error
	if h.LoadIndex != nil {
		ix, err = h.LoadIndex(ctx, root)
		if err != nil {
			return "", err
		}
	} else {
		ix = &index.Index{Root: root}
	}

	src := runtime.LoadSource(ix.Root)

	switch action {
	case "status":
		return jsonOf(runtime.StatusSnapshot(src))
	case "drift":
		var codeRoutes []string
		for _, sym := range ix.Symbols {
			if sym.Route != "" {
				codeRoutes = append(codeRoutes, sym.Route)
			}
		}
		return jsonOf(runtime.DriftSnapshot(src, codeRoutes))
	case "routes":
		if src == nil {
			return jsonOf([]map[string]any{})
		}
		return jsonOf(runtime.Routes(src))
	case "events":
		if src == nil {
			return jsonOf([]runtime.Event{})
		}
		service := mcpargs.ArgString(args, "service")
		return jsonOf(src.Events(service))
	case "correlate":
		if src == nil {
			return jsonOf(map[string]any{
				"wired": false,
				"error": "no runtime source available to correlate",
			})
		}
		service := mcpargs.ArgString(args, "service")
		message := mcpargs.ArgString(args, "message")
		if message == "" {
			message = mcpargs.ArgString(args, "title")
		}
		if message == "" {
			message = "runtime incident"
		}
		sevStr := mcpargs.ArgString(args, "severity")
		sev := domain.SeverityWarning
		switch sevStr {
		case "critical":
			sev = domain.SeverityCritical
		case "error", "high":
			sev = domain.SeverityError
		case "info", "low":
			sev = domain.SeverityInfo
		}
		window := 30 * time.Minute
		if wStr := mcpargs.ArgString(args, "window"); wStr != "" {
			if d, err := time.ParseDuration(wStr); err == nil && d > 0 {
				window = d
			}
		}
		occurredAt := time.Now()
		if tsStr := mcpargs.ArgString(args, "occurred_at"); tsStr != "" {
			if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
				occurredAt = t
			}
		}
		alert := domain.Alert{
			Message:    message,
			Service:    service,
			Severity:   sev,
			OccurredAt: occurredAt,
		}
		correlator := runtime.NewCorrelator(src, window)
		res := correlator.Correlate(alert)
		return jsonOf(map[string]any{
			"alert":            res.Alert,
			"affected_service": res.AffectedService,
			"severity":         res.Severity,
			"deployments":      res.Deployments,
			"recent_commits":   res.RecentCommits,
			"error_events":     res.ErrorEvents,
			"log_events":       res.LogEvents,
			"trace_spans":      res.TraceSpans,
			"metric_events":    res.MetricEvents,
			"window_seconds":   int(res.Window.Seconds()),
		})
	default:
		return "", fmt.Errorf("kern_runtime: unknown action %q (status|drift|routes|events|correlate)", action)
	}
}
