package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// handleSemanticDiff returns an AST-level symbol diff instead of raw lines.
// Highlights changed functions, modified signatures, and newly impacted callers,
// giving AI agents precise functional insight without burning tokens on syntax noise.
func (s *Server) handleSemanticDiff(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	from := argString(args, "from")
	to := argString(args, "to")

	if r := argString(args, "range"); r != "" {
		if parts := strings.SplitN(r, "..", 2); len(parts) == 2 {
			from = parts[0]
			to = parts[1]
		} else {
			from = r
		}
	}

	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	report, err := intel.SemanticDiff(ix, root, from, to)
	if err != nil {
		return "", err
	}

	return report.Render(), nil
}
