package contextwatch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Analysis is the structured audit of current session context and token usage.
type Analysis struct {
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

// Analyze analyzes text or conversational history, breaks it down by token cost,
// identifies bloated segments, and returns a formatted report or JSON.
func Analyze(text string, budgetTokens int, format string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text or context argument is required")
	}

	if budgetTokens <= 0 {
		budgetTokens = 32000
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

	blocks := strings.Split(text, "\n\n")
	var heavySegments []HeavySegment
	estTotalSaved := 0

	if len(blocks) <= 2 && totalTokens > 100 {
		blocks = []string{text}
	}

	for i, block := range blocks {
		t := strings.TrimSpace(block)
		if len(t) == 0 {
			continue
		}
		toks := tokenize.Count(t)
		if toks < 40 {
			continue
		}

		pct := (float64(toks) / float64(totalTokens)) * 100
		kind := "prose"
		suggestedTool := "kern_optimize_prompt"
		estGain := int(float64(toks) * 0.35)

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

	analysis := Analysis{
		TotalTokens:     totalTokens,
		TokenBudget:     budgetTokens,
		BudgetUsagePct:  usagePct,
		BloatSeverity:   severity,
		HeavySegments:   heavySegments,
		Recommendations: recs,
		EstimatedSaved:  estTotalSaved,
	}

	if format == "json" {
		data, err := json.MarshalIndent(analysis, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

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
