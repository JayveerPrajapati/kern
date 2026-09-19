package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/crossrepo"
)

// handleCrossRepoImpact traces external call sites across all registered repositories in RepoRegistry.
func (s *Server) handleCrossRepoImpact(ctx context.Context, args map[string]any) (string, error) {
	subject := argString(args, "target_symbol")
	if subject == "" {
		subject = argString(args, "subject")
	}
	if subject == "" {
		subject = argString(args, "symbol")
	}
	if subject == "" {
		return "", fmt.Errorf("target_symbol, subject, or symbol is required")
	}

	limit := 20
	if lStr := argString(args, "limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}

	format := strings.ToLower(argString(args, "format"))
	return crossrepo.Impact(subject, limit, format)
}
