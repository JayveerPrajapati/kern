// Package memory owns project brain memory MCP tool bodies (kern_memory_*, kern_memory_ranked)
// as plain functions.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	Add    func(ctx context.Context, root, lesson string) error
	List   func(ctx context.Context, root string) ([]memory.Entry, error)
	Recall func(ctx context.Context, root, prompt string, k int) ([]memory.Entry, error)
}

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// Add stores a new lesson in project memory.
func Add(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	lesson := mcpargs.ArgString(args, "lesson")
	if lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	if h.Add != nil {
		if err := h.Add(ctx, root, lesson); err != nil {
			return "", err
		}
	} else {
		if err := memory.Add(root, lesson); err != nil {
			return "", err
		}
	}
	return "remembered.", nil
}

// List lists all lessons stored in project memory.
func List(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	var entries []memory.Entry
	var err error
	if h.List != nil {
		entries, err = h.List(ctx, root)
	} else {
		entries = memory.List(root)
	}
	if err != nil {
		return "", err
	}
	return memory.FormatEntries(entries), nil
}

// Recall searches project memory for lessons relevant to a prompt.
func Recall(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	prompt := mcpargs.ArgString(args, "prompt")
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	k := memory.DefaultRecallLimit
	limitStr := mcpargs.ArgString(args, "limit")
	if limitStr == "" {
		limitStr = mcpargs.ArgString(args, "k") // backward-compat alias
	}
	if v := limitStr; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("limit: invalid integer %q", v)
		}
		if n > 0 {
			k = n
		}
	}
	var entries []memory.Entry
	var err error
	if h.Recall != nil {
		entries, err = h.Recall(ctx, root, prompt, k)
	} else {
		entries = memory.Recall(root, prompt, k)
	}
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return memory.NoRecallMatch, nil
	}
	return memory.FormatEntries(entries), nil
}

// Action handles unified memory action dispatch (add, list, recall).
func Action(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	switch action {
	case "add":
		lesson := mcpargs.ArgString(args, "lesson")
		if lesson == "" {
			return "", fmt.Errorf("lesson is required for action 'add'")
		}
		if h.Add != nil {
			if err := h.Add(ctx, root, lesson); err != nil {
				return "", err
			}
		} else {
			if err := memory.Add(root, lesson); err != nil {
				return "", err
			}
		}
		return "remembered.", nil
	case "list":
		var entries []memory.Entry
		var err error
		if h.List != nil {
			entries, err = h.List(ctx, root)
		} else {
			entries = memory.List(root)
		}
		if err != nil {
			return "", err
		}
		return memory.FormatEntries(entries), nil
	case "recall":
		prompt := mcpargs.ArgString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required for action 'recall'")
		}
		var entries []memory.Entry
		var err error
		if h.Recall != nil {
			entries, err = h.Recall(ctx, root, prompt, memory.DefaultRecallLimit)
		} else {
			entries = memory.Recall(root, prompt, memory.DefaultRecallLimit)
		}
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return memory.NoRecallMatch, nil
		}
		return memory.FormatEntries(entries), nil
	default:
		return "", fmt.Errorf("unknown memory action %q (want add, list, or recall)", action)
	}
}

// Ranked handles exponential-decay recency/relevance ranked memory retrieval.
func Ranked(ctx context.Context, args map[string]any) (string, error) {
	prompt := mcpargs.ArgString(args, "prompt")
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("kern_memory_ranked: 'prompt' is required")
	}

	root := resolveRoot(mcpargs.ArgString(args, "root"))
	k := 5
	if kStr := mcpargs.ArgString(args, "k"); kStr != "" {
		if n, err := strconv.Atoi(kStr); err == nil && n > 0 {
			k = n
		}
	} else if kv, ok := args["k"].(float64); ok && kv > 0 {
		k = int(kv)
	}

	halfLife := 7.0
	if hlStr := mcpargs.ArgString(args, "half_life_days"); hlStr != "" {
		if f, err := strconv.ParseFloat(hlStr, 64); err == nil && f > 0 {
			halfLife = f
		}
	} else if hlv, ok := args["half_life_days"].(float64); ok && hlv > 0 {
		halfLife = hlv
	}

	ranked := memory.RecallRanked(root, prompt, k, halfLife)

	format := strings.ToLower(mcpargs.ArgString(args, "format"))
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
