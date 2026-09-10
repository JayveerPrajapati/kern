package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/budget"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func (s *Server) handleContextEnvelope(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
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
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
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
		if argBool(args, "with_freshness") {
			if ix, err := s.loadIndex(ctx, root); err == nil {
				out = append(out, s.freshnessFooter(args, ix)...)
			}
		}
		return string(out), nil

	}
}
