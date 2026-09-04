package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"strconv"
	"strings"
)

func (s *Server) handleOptimizePrompt(ctx context.Context, args map[string]any) (string, error) {
	{
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		mask := argString(args, "mask") == "true" || argString(args, "mask") == "1"
		var names []string
		for _, n := range strings.Split(argString(args, "mask_names"), ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		cacheOn := true
		if v := argString(args, "cache"); v != "" {
			cacheOn = v == "true" || v == "1"
		}
		res, err := optimize.Prompt(prompt, argString(args, "attached_log"), optimize.Options{
			Session:   argString(args, "session"),
			Model:     argString(args, "model"),
			Mask:      mask,
			MaskNames: names,
			Cache:     cacheOn,
			FewShot:   argString(args, "few_shot") == "true" || argString(args, "few_shot") == "1",
			Root:      argString(args, "root"),
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
}

func (s *Server) handleSwap(ctx context.Context, args map[string]any) (string, error) {
	{
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		mode := argString(args, "mode")
		switch mode {
		case "summary":
			return swap.SummaryMode(text, root), nil
		case "expand":
			return swap.ExpandMode(text, root), nil
		default:
			maxTok := 0
			if s := argString(args, "max_tokens"); s != "" {
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
}

func (s *Server) handleOptimizeLog(ctx context.Context, args map[string]any) (string, error) {
	{
		log := argString(args, "log")
		if log == "" {
			return "", fmt.Errorf("log is required")
		}
		cacheOn := true
		if v := argString(args, "cache"); v != "" {
			cacheOn = v == "true" || v == "1"
		}
		res, err := optimize.Log(log, optimize.Options{Cache: cacheOn})
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
		return out, nil

	}
}

func (s *Server) handleOptimizeOutput(ctx context.Context, args map[string]any) (string, error) {
	{
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		out, dropped := terse.Compress(text)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped)\n\n%s",
			before, after, before-after, strutil.Pct(before, after), dropped, out), nil

	}
}

func (s *Server) handleStats(ctx context.Context, args map[string]any) (string, error) {
	{
		return renderStats(argString(args, "days"), argString(args, "session"))

	}
}

func (s *Server) handleSemcache(ctx context.Context, args map[string]any) (string, error) {
	{
		switch argString(args, "action") {
		case "clear":
			ns := argString(args, "namespace")
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
			ns := argString(args, "namespace")
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
			a, b := argString(args, "a"), argString(args, "b")
			if a == "" || b == "" {
				return "", fmt.Errorf("a and b are required for similarity")
			}
			return fmt.Sprintf("similarity: %.3f", semcache.Similarity(a, b)), nil
		default: // stats
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
}

func (s *Server) handleContextBudget(ctx context.Context, args map[string]any) (string, error) {
	{
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		out := budget.FitCode(text, maxTokens)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%)\n\n%s", before, after, before-after, strutil.Pct(before, after), out), nil

	}
}
