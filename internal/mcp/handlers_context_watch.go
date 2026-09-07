package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// ContextWatchAnalysis is the structured audit of current session context and token usage.
type ContextWatchAnalysis struct {
	TotalTokens     int            `json:"total_tokens"`
	TokenBudget     int            `json:"token_budget"`
	BudgetUsagePct  float64        `json:"budget_usage_pct"`
	BloatSeverity   string         `json:"bloat_severity"` // "OK" | "WARNING" | "CRITICAL"
	HeavySegments   []HeavySegment `json:"heavy_segments"`
	Recommendations []string       `json:"recommendations"`
	EstimatedSaved  int            `json:"estimated_tokens_saved_if_optimized"`
}

// HeavySegment represents a segment of text or tool output that is consuming excessive context.
type HeavySegment struct {
	Index          int     `json:"index"`
	Kind           string  `json:"kind"` // "code" | "log" | "prose"
	Tokens         int     `json:"tokens"`
	PctOfTotal     float64 `json:"pct_of_total"`
	SuggestedTool  string  `json:"suggested_tool"`
	EstimatedGain  int     `json:"estimated_gain_tokens"`
	SnippetPreview string  `json:"snippet_preview"`
}

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

	totalTokens := tokenize.Count(text)
	usagePct := 0.0
	if budgetTokens > 0 {
		usagePct = (float64(totalTokens) / float64(budgetTokens)) * 100
	}

	severity := "OK"
	if usagePct >= 90 {
		severity = "CRITICAL"
	} else if usagePct >= 70 {
		severity = "WARNING"
	}

	// Split text into candidate blocks (by double newline, triple newline, or treat whole text as segment if single block)
	blocks := strings.Split(text, "\n\n")
	var heavySegments []HeavySegment
	estTotalSaved := 0

	// If fewer than 2 blocks and text is large, treat text itself as a segment
	if len(blocks) <= 2 && totalTokens > 100 {
		blocks = []string{text}
	}

	for i, block := range blocks {
		t := strings.TrimSpace(block)
		if len(t) == 0 {
			continue
		}
		toks := tokenize.Count(t)
		if toks < 40 { // Ignore tiny snippets
			continue
		}

		pct := (float64(toks) / float64(totalTokens)) * 100
		kind := "prose"
		suggestedTool := "kern_optimize_prompt"
		estGain := int(float64(toks) * 0.35)

		// Detect if log-heavy
		if strings.Contains(t, "ERROR") || strings.Contains(t, "WARN") || strings.Contains(t, "Traceback") || strings.Contains(t, "panic:") {
			kind = "log"
			suggestedTool = "kern_optimize_log"
			estGain = int(float64(toks) * 0.60)
		} else if strings.Contains(t, "package ") || strings.Contains(t, "func ") || strings.Contains(t, "class ") || strings.Contains(t, "def ") {
			kind = "code"
			suggestedTool = "kern_compact_file"
			estGain = int(float64(toks) * 0.50)
		}

		estTotalSaved += estGain
		preview := t
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}

		heavySegments = append(heavySegments, HeavySegment{
			Index:          i + 1,
			Kind:           kind,
			Tokens:         toks,
			PctOfTotal:     pct,
			SuggestedTool:  suggestedTool,
			EstimatedGain:  estGain,
			SnippetPreview: preview,
		})
	}

	var recs []string
	if severity == "CRITICAL" {
		recs = append(recs, fmt.Sprintf("Context is at %.1f%% capacity! Apply pruning immediately to avoid context overflow.", usagePct))
	} else if severity == "WARNING" {
		recs = append(recs, fmt.Sprintf("Context is reaching high water mark (%.1f%%). Consider compressing heavy segments.", usagePct))
	} else {
		recs = append(recs, "Context utilization is healthy.")
	}

	for _, seg := range heavySegments {
		if seg.Kind == "log" {
			recs = append(recs, fmt.Sprintf("Segment #%d is a raw log (%d tokens). Run %s to reduce by ~%d tokens.", seg.Index, seg.Tokens, seg.SuggestedTool, seg.EstimatedGain))
		} else if seg.Kind == "code" {
			recs = append(recs, fmt.Sprintf("Segment #%d is verbatim code (%d tokens). Use %s or kern_context to slice symbols instead.", seg.Index, seg.Tokens, seg.SuggestedTool))
		}
	}

	analysis := ContextWatchAnalysis{
		TotalTokens:     totalTokens,
		TokenBudget:     budgetTokens,
		BudgetUsagePct:  usagePct,
		BloatSeverity:   severity,
		HeavySegments:   heavySegments,
		Recommendations: recs,
		EstimatedSaved:  estTotalSaved,
	}

	if argString(args, "format") == "json" {
		data, _ := json.MarshalIndent(analysis, "", "  ")
		return string(data), nil
	}

	// Plain text report
	var b strings.Builder
	fmt.Fprintf(&b, "CONTEXT WATCH REPORT [%s — %.1f%% of %d token budget used]\n", severity, usagePct, budgetTokens)
	fmt.Fprintf(&b, "=================================================================\n")
	fmt.Fprintf(&b, "Current Context Size: %d tokens | Potential Optimization Savings: ~%d tokens\n\n", totalTokens, estTotalSaved)

	if len(heavySegments) > 0 {
		fmt.Fprintf(&b, "Bloated Context Segments Identified (%d):\n", len(heavySegments))
		for _, seg := range heavySegments {
			fmt.Fprintf(&b, "  • [%s] Segment #%d: %d tokens (%.1f%% of total)\n", strings.ToUpper(seg.Kind), seg.Index, seg.Tokens, seg.PctOfTotal)
			fmt.Fprintf(&b, "    Preview: %s\n", seg.SnippetPreview)
			fmt.Fprintf(&b, "    Action: Run %s (Estimated gain: ~%d tokens)\n\n", seg.SuggestedTool, seg.EstimatedGain)
		}
	} else {
		fmt.Fprintln(&b, "No individual runaway segments found. All context chunks are within reasonable bounds.")
	}

	fmt.Fprintf(&b, "Recommendations:\n")
	for _, r := range recs {
		fmt.Fprintf(&b, "  → %s\n", r)
	}

	return strings.TrimSpace(b.String()), nil
}
