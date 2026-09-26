// Package envelope owns the context-envelope MCP tool body
// (kern_context_envelope) as a plain function with a Hooks bundle injected
// by the mcp adapter.
package envelope

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/budget"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Hooks carries the kernel callbacks ContextEnvelope needs: the platform
// (analyze pipeline), the session index loader and the freshness footer
// used by the opt-in freshness proof. Neither callback touches the service
// layer, so no other hooks are needed.
type Hooks struct {
	PlatformFor     func(ctx context.Context, root string) (*app.Platform, error)
	LoadIndex       func(ctx context.Context, root string) (*index.Index, error)
	FreshnessFooter func(args map[string]any, ix *index.Index) string
}

// ContextEnvelope renders the context envelope for a change, optionally
// budget-fitted to max_tokens and annotated with an opt-in freshness proof.
func ContextEnvelope(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	p, err := h.PlatformFor(ctx, root)
	if err != nil {
		return "", err
	}
	pkt, _, err := p.Analyze(change)
	if err != nil {
		return "", err
	}
	pkt.EnvelopeVersion = domain.EnvelopeVersionV1
	if pkt.SchemaVersion == "" {
		pkt.SchemaVersion = "1.0.0"
	}
	maxTokens := 0
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}
	if maxTokens > 0 && pkt.FittedText == "" {
		pkt.FittedText = budget.Fit(kernctx.RenderText(pkt), maxTokens)
		pkt.TokenCount = tokenize.Count(pkt.FittedText)
	}
	out, err := json.MarshalIndent(pkt, "", "  ")
	if err != nil {
		return "", err
	}
	// Freshness proof is opt-in and best-effort: only when the index loads
	// cheaply. A load failure never fails the envelope call.
	if mcpargs.ArgBool(args, "with_freshness") {
		if h.LoadIndex != nil && h.FreshnessFooter != nil {
			if ix, err := h.LoadIndex(ctx, root); err == nil {
				out = append(out, h.FreshnessFooter(args, ix)...)
			}
		}
	}
	return string(out), nil
}
