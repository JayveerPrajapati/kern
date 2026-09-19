package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/mcp/preedit"
)

// handlePreEdit performs predictive blast-radius, caller sensitivity, and test-gap analysis
// BEFORE code is written or edited. Agents call this to know exactly what can break and which
// callers must be verified before making changes.
func (s *Server) handlePreEdit(ctx context.Context, args map[string]any) (string, error) {
	file := argString(args, "file")
	symbol := argString(args, "symbol")
	linesStr := argString(args, "lines")
	root := resolveRoot(argString(args, "root"))

	if file == "" && symbol == "" {
		return "", fmt.Errorf("at least one of 'file' or 'symbol' must be provided")
	}

	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	return preedit.Analyze(ctx, ix, root, file, symbol, linesStr)
}
