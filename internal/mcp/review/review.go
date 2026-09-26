// Package review owns the review-family MCP tool bodies (kern_changes,
// kern_review, kern_hubs, kern_test_gaps) as plain functions.
package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	LoadIndex      func(ctx context.Context, root string) (*index.Index, error)
	ChangedContext func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error)
}

// Changes renders architectural and semantic analysis for changed files.
func Changes(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	changes, ix, err := h.ChangedContext(ctx, args)
	if err != nil {
		return "", err
	}
	return intel.RenderChanges(intel.AnalyzeChangesRanged(ix, changes)), nil
}

// Review generates prioritized code review context with lenses and runtime overlays.
func Review(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	changes, ix, err := h.ChangedContext(ctx, args)
	if err != nil {
		return "", err
	}
	maxTokens := 8000
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	overlays := []func(file string) string(nil)
	if src := runtime.LoadSource(ix.Root); src != nil {
		overlays = append(overlays, runtime.Overlay(src))
	}
	var out string
	if lensName := mcpargs.ArgString(args, "lens"); lensName != "" {
		l, err := lenses.Resolve(lensName)
		if err != nil {
			return "", err
		}
		out = intel.ReviewRangedWithLens(ix, changes, maxTokens, l.Name, lenses.RenderPriorities(l), overlays...)
	} else {
		out = intel.ReviewRanged(ix, changes, maxTokens, overlays...)
	}
	if profileName := mcpargs.ArgString(args, "profile"); profileName != "" {
		p, ok := profiles.NewRegistryWithUserProfiles(ix.Root).Select(profileName)
		if !ok {
			return "", fmt.Errorf("unknown profile %q", profileName)
		}
		out = profiles.ApplyProfile(p, out)
	}
	return out, nil
}

// Hubs renders topological centrality hubs and bridges in the dependency graph.
func Hubs(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	ix, err := h.LoadIndex(ctx, mcpargs.ArgString(args, "root"))
	if err != nil {
		return "", err
	}
	limit := 10
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	var b strings.Builder
	b.WriteString(intel.RenderHubs(intel.Hubs(ix, limit)))
	b.WriteString("\n\n")
	b.WriteString(intel.RenderBridges(intel.Bridges(ix, 15)))
	return b.String(), nil
}

// TestGaps identifies untested hot paths and coverage gaps.
func TestGaps(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	ix, err := h.LoadIndex(ctx, mcpargs.ArgString(args, "root"))
	if err != nil {
		return "", err
	}
	limit := 10
	if v := mcpargs.ArgString(args, "limit"); v != "" {
		n, err := mcpargs.AtoiArg(v, limit)
		if err != nil {
			return "", err
		}
		limit = n
	}
	c := intel.AnalyzeCoverage(ix)
	c.HotGaps = intel.TestGaps(ix, limit)
	return c.RenderLimited(limit), nil
}
