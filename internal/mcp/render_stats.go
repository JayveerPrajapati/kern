// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/stats"
)

// clipStatsMarker shortens a matched-input preview so a multi-megabyte log
// match never floods the "served from semantic cache" marker. Deliberately
// distinct from optimize.ClipOptimize (newline-flattening, 60-byte "..."
// suffix): the two clip policies are behaviorally different and must never
// be conflated.
func clipStatsMarker(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func renderStats(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	sum, err := rec.Summarize(days, session)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("operations=%d before=%d after=%d saved=%d (%.1f%%) cost_saved=$%.4f",
		sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved), nil
}

// renderStatsByTool renders the per-tool token ledger for the kern_stats MCP
// tool (by_tool=true): the same table `kern stats --by-tool` prints.
func renderStatsByTool(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	tools, err := rec.SummarizeByTool(days, session)
	if err != nil {
		return "", err
	}
	if len(tools) == 0 {
		return "no per-tool data yet (MCP tool calls and kern compact record entries)", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %6s %14s %13s %11s\n", "tool", "calls", "tokens ret", "tokens saved", "cost saved")
	for _, ts := range tools {
		fmt.Fprintf(&b, "%-22s %6d %14d %13d $%10.4f\n", ts.Tool, ts.Calls, ts.TokensReturned, ts.TokensSaved, ts.CostSaved)
	}
	return b.String(), nil
}
