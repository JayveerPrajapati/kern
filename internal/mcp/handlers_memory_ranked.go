package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func (s *Server) handleMemoryRanked(ctx context.Context, args map[string]any) (string, error) {
	prompt := argString(args, "prompt")
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("kern_memory_ranked: 'prompt' is required")
	}

	root := resolveRoot(argString(args, "root"))
	k := 5
	if kStr := argString(args, "k"); kStr != "" {
		if n, err := strconv.Atoi(kStr); err == nil && n > 0 {
			k = n
		}
	} else if kv, ok := args["k"].(float64); ok && kv > 0 {
		k = int(kv)
	}

	halfLife := 7.0
	if hlStr := argString(args, "half_life_days"); hlStr != "" {
		if f, err := strconv.ParseFloat(hlStr, 64); err == nil && f > 0 {
			halfLife = f
		}
	} else if hlv, ok := args["half_life_days"].(float64); ok && hlv > 0 {
		halfLife = hlv
	}

	ranked := memory.RecallRanked(root, prompt, k, halfLife)

	format := strings.ToLower(argString(args, "format"))
	if format == "json" {
		type RankedView struct {
			Text      string  `json:"text"`
			Time      string  `json:"time"`
			Score     float64 `json:"score"`
			Relevance float64 `json:"relevance"`
			Recency   float64 `json:"recency"`
			AgeHours  float64 `json:"age_hours"`
		}
		view := make([]RankedView, len(ranked))
		for i, r := range ranked {
			view[i] = RankedView{
				Text:      r.Entry.Text,
				Time:      r.Entry.Time.Format("2006-01-02 15:04:05"),
				Score:     r.Score,
				Relevance: r.Relevance,
				Recency:   r.Recency,
				AgeHours:  r.AgeHours,
			}
		}
		data, _ := json.MarshalIndent(map[string]any{
			"prompt":         prompt,
			"half_life_days": halfLife,
			"count":          len(ranked),
			"lessons":        view,
		}, "", "  ")
		return string(data), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Decay-Ranked Memory Retrieval (Half-Life: %.1fd)\n\n", halfLife))
	sb.WriteString(fmt.Sprintf("**Query Prompt:** %s\n", prompt))
	sb.WriteString(fmt.Sprintf("**Matches Found:** %d\n\n", len(ranked)))

	if len(ranked) == 0 {
		sb.WriteString("_No matching historical lessons found in project memory._\n")
	} else {
		sb.WriteString("| Rank | Score | Rel | Recency | Age | Lesson |\n")
		sb.WriteString("|---|---|---|---|---|---|\n")
		for i, r := range ranked {
			text := r.Entry.Text
			if len(text) > 80 {
				text = text[:77] + "..."
			}
			text = strings.ReplaceAll(text, "|", "\\|")
			text = strings.ReplaceAll(text, "\n", " ")
			ageStr := fmt.Sprintf("%.1fh", r.AgeHours)
			if r.AgeHours > 24 {
				ageStr = fmt.Sprintf("%.1fd", r.AgeHours/24.0)
			}
			sb.WriteString(fmt.Sprintf("| #%d | %.3f | %.2f | %.2f | %s | %s |\n",
				i+1, r.Score, r.Relevance, r.Recency, ageStr, text))
		}
	}

	return sb.String(), nil
}
