package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// handleExplain produces an end-to-end architecture explanation for a symbol or subsystem.
// Combines declaration location, direct callers, callees, and downstream execution chain into
// a single cohesive response, preventing agents from making 5+ separate discovery calls.
func (s *Server) handleExplain(ctx context.Context, args map[string]any) (string, error) {
	subject := argString(args, "target")
	if subject == "" {
		subject = argString(args, "subject")
	}
	if subject == "" {
		subject = argString(args, "symbol")
	}
	if subject == "" {
		return "", fmt.Errorf("target, subject or symbol is required")
	}

	root := resolveRoot(argString(args, "root"))
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	exp, err := intel.Explain(ix, subject)
	if err != nil {
		return "", err
	}

	return exp.Render(), nil
}
