package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

func (s *Server) handleChanges(ctx context.Context, args map[string]any) (string, error) {
	{
		changes, ix, err := s.changedContext(ctx, args)
		if err != nil {
			return "", err
		}
		return intel.RenderChanges(intel.AnalyzeChangesRanged(ix, changes)), nil

	}
}

func (s *Server) handleReview(ctx context.Context, args map[string]any) (string, error) {
	{
		changes, ix, err := s.changedContext(ctx, args)
		if err != nil {
			return "", err
		}
		maxTokens := 8000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		// The runtime-aware review gate: when a runtime source is wired for
		// the project, each changed file gets its service profile overlay
		// (rps/error rate, FLAG above the threshold). No source -> identical
		// output to before.
		overlays := []func(file string) string(nil)
		if src := runtime.LoadSource(ix.Root); src != nil {
			overlays = append(overlays, runtime.Overlay(src))
		}
		var out string
		// Review lens: a named lens prepends its evidence-priority
		// line so the caller knows which review posture the context is sized
		// for. No lens arg -> byte-identical output. Combined names
		// ("security+maintainability", "security,maintainability") resolve to
		// a merged preset via lenses.Resolve; the header now lives inside
		// intel.ReviewRangedWithLens.
		if lensName := argString(args, "lens"); lensName != "" {
			l, err := lenses.Resolve(lensName)
			if err != nil {
				return "", err
			}
			out = intel.ReviewRangedWithLens(ix, changes, maxTokens, l.Name, lenses.RenderPriorities(l), overlays...)
		} else {
			out = intel.ReviewRanged(ix, changes, maxTokens, overlays...)
		}
		if profileName := argString(args, "profile"); profileName != "" {
			p, ok := profiles.NewRegistryWithUserProfiles(ix.Root).Select(profileName)
			if !ok {
				return "", fmt.Errorf("unknown profile %q", profileName)
			}
			out = profiles.ApplyProfile(p, out)
		}
		return out, nil

	}
}

func (s *Server) handleHubs(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
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
}

func (s *Server) handleTestGaps(ctx context.Context, args map[string]any) (string, error) {
	{
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		c := intel.AnalyzeCoverage(ix)
		c.HotGaps = intel.TestGaps(ix, limit)
		return c.Render(), nil

	}
}
