// Package optimize owns the prompt and log optimization MCP tool bodies
// (kern_optimize_prompt, kern_swap, kern_optimize_log, kern_optimize_output,
// kern_semcache, kern_context_budget, kern_fetch_raw_anchor) as plain functions.
package optimize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

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

func clipForMarker(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func validScope(scope string) bool {
	if scope == "" {
		return false
	}
	if strings.HasPrefix(scope, ".") {
		return false
	}
	for _, r := range scope {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func renderOptimize(title string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		title, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
}

// Prompt compresses, masks secrets, and optimizes prompts using project memory.
func Prompt(ctx context.Context, args map[string]any) (string, error) {
	prompt := mcpargs.ArgString(args, "prompt")
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	mask := mcpargs.ArgString(args, "mask") == "true" || mcpargs.ArgString(args, "mask") == "1"
	var names []string
	for _, n := range strings.Split(mcpargs.ArgString(args, "mask_names"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	cacheOn := true
	if v := mcpargs.ArgString(args, "cache"); v != "" {
		cacheOn = v == "true" || v == "1"
	}
	res, err := optimize.Prompt(prompt, mcpargs.ArgString(args, "attached_log"), optimize.Options{
		Session:   mcpargs.ArgString(args, "session"),
		Model:     mcpargs.ArgString(args, "model"),
		Mask:      mask,
		MaskNames: names,
		Cache:     cacheOn,
		FewShot:   mcpargs.ArgString(args, "few_shot") == "true" || mcpargs.ArgString(args, "few_shot") == "1",
		Root:      resolveRoot(mcpargs.ArgString(args, "root")),
	})
	if err != nil {
		return "", err
	}
	out := renderOptimize("optimized prompt", res)
	if res.FromCache {
		if res.SemanticHit {
			out += fmt.Sprintf("\n[kern] served from semantic cache (similarity %.2f, matched: %q)\n", res.Similarity, clipForMarker(res.MatchedInput))
		} else {
			out += "\n[kern] served from exact cache\n"
		}
	}
	return out, nil
}

// Swap swaps raw source with concise signatures or expands summaries.
func Swap(ctx context.Context, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	mode := mcpargs.ArgString(args, "mode")
	switch mode {
	case "summary":
		return swap.SummaryMode(text, root), nil
	case "expand":
		return swap.ExpandMode(text, root), nil
	default:
		maxTok := 0
		if s := mcpargs.ArgString(args, "max_tokens"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("max_tokens: invalid integer %q", s)
			}
			if n > 0 {
				maxTok = n
			}
		}
		out, fits := swap.Fit(text, root, maxTok)
		if !fits {
			out += "\n[kern] warning: still over budget after summarization\n"
		}
		return out, nil
	}
}

// Log compresses redundant log lines and stack traces.
func Log(ctx context.Context, args map[string]any) (string, error) {
	log := mcpargs.ArgString(args, "log")
	if log == "" {
		return "", fmt.Errorf("log is required")
	}
	cacheOn := true
	if v := mcpargs.ArgString(args, "cache"); v != "" {
		cacheOn = v == "true" || v == "1"
	}
	ctxBefore, _ := mcpargs.ArgInt(args, "context_before", 0)
	ctxAfter, _ := mcpargs.ArgInt(args, "context_after", 0)
	profile := mcpargs.ArgString(args, "profile")
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	structMarkers := mcpargs.ArgString(args, "structured_markers") == "true" || mcpargs.ArgString(args, "structured_markers") == "1"
	res, err := optimize.Log(log, optimize.Options{
		Cache:             cacheOn,
		ContextBefore:     ctxBefore,
		ContextAfter:      ctxAfter,
		Profile:           profile,
		Root:              root,
		StructuredMarkers: structMarkers,
	})
	if err != nil {
		return "", err
	}
	out := renderOptimize("optimized log", res)
	if res.FromCache {
		if res.SemanticHit {
			out += fmt.Sprintf("\n[kern] served from semantic cache (similarity %.2f, matched: %q)\n", res.Similarity, clipForMarker(res.MatchedInput))
		} else {
			out += "\n[kern] served from exact cache\n"
		}
	}
	if ctxBefore > 0 || ctxAfter > 0 {
		out += fmt.Sprintf("\n[kern] adaptive window: -%d lines before / +%d lines after each error event\n", ctxBefore, ctxAfter)
	}
	return out, nil
}

// Output compresses verbose tool outputs.
func Output(ctx context.Context, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	out, dropped := terse.Compress(text)
	before := tokenize.Count(text)
	after := tokenize.Count(out)
	return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped)\n\n%s",
		before, after, before-after, strutil.Pct(before, after), dropped, out), nil
}

// Semcache manages semantic caching operations.
func Semcache(ctx context.Context, args map[string]any) (string, error) {
	switch mcpargs.ArgString(args, "action") {
	case "clear":
		ns := mcpargs.ArgString(args, "namespace")
		if ns != "" && !validScope(ns) {
			return "", fmt.Errorf("invalid semcache namespace %q: must contain only [A-Za-z0-9._-] and not start with '.'", ns)
		}
		if err := semcache.Clear(ns); err != nil {
			return "", err
		}
		if ns == "" {
			return "semcache: cleared all namespaces", nil
		}
		return "semcache: cleared " + ns, nil
	case "list":
		ns := mcpargs.ArgString(args, "namespace")
		if ns == "" {
			return "", fmt.Errorf("namespace is required for list")
		}
		entries, err := semcache.Entries(ns)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return fmt.Sprintf("semcache %q: empty", ns), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "semcache %q: %d entries\n", ns, len(entries))
		for i, in := range entries {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, truncateMCP(in, 100))
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	case "similarity":
		a, b := mcpargs.ArgString(args, "a"), mcpargs.ArgString(args, "b")
		if a == "" || b == "" {
			return "", fmt.Errorf("a and b are required for similarity")
		}
		return fmt.Sprintf("similarity: %.3f", semcache.Similarity(a, b)), nil
	default:
		st, err := semcache.Stats()
		if err != nil {
			return "", err
		}
		if len(st) == 0 {
			return "semcache: empty", nil
		}
		var b strings.Builder
		b.WriteString("semcache entries by namespace:\n")
		for ns, n := range st {
			fmt.Fprintf(&b, "  %-8s %d\n", ns, n)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	}
}

// ContextBudget fits code within a token budget limit.
func ContextBudget(ctx context.Context, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	maxTokens := 4000
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}
	out := budget.FitCode(text, maxTokens)
	before := tokenize.Count(text)
	after := tokenize.Count(out)
	return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%)\n\n%s", before, after, before-after, strutil.Pct(before, after), out), nil
}

// FetchAnchor retrieves the raw verbatim text of an anchor.
func FetchAnchor(ctx context.Context, args map[string]any) (string, error) {
	id := mcpargs.ArgString(args, "anchor_id")
	if id == "" {
		id = mcpargs.ArgString(args, "id")
	}
	if id == "" {
		return "", fmt.Errorf("anchor_id is required")
	}
	return optimize.FetchAnchor(id)
}
