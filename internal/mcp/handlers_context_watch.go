package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/contextwatch"
)

// ContextWatchAnalysis is the structured audit of current session context and token usage.
type ContextWatchAnalysis = contextwatch.Analysis

// HeavySegment represents a segment of text or tool output that is consuming excessive context.
type HeavySegment = contextwatch.HeavySegment

// handleContextWatch analyzes an agent's active context or conversational history,
// breaks it down by token cost, identifies bloated segments (e.g. raw logs, huge code dumps),
// and provides proactive, deterministic pruning actions before the agent overflows its context window.
func (s *Server) handleContextWatch(ctx context.Context, args map[string]any) (string, error) {
	text := argString(args, "text")
	if text == "" {
		text = argString(args, "context")
	}
	if text == "" {
		return "", fmt.Errorf("text or context argument is required")
	}

	budgetTokens := 32000
	if bStr := argString(args, "budget"); bStr != "" {
		var b int
		if _, err := fmt.Sscanf(bStr, "%d", &b); err == nil && b > 0 {
			budgetTokens = b
		}
	}

	format := strings.ToLower(argString(args, "format"))
	return contextwatch.Analyze(text, budgetTokens, format)
}
