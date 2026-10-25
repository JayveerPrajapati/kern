package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/security"
	"github.com/JayveerPrajapati/kern/internal/pii"
)

// securitySvc implements security.SecurityService directly over the internal
// engines — the thin facade the dissolved internal/service layer used to
// provide, kept local to the MCP adapter.
type securitySvc struct{}

func (securitySvc) Mask(ctx context.Context, text string, names []string) (pii.Result, error) {
	return pii.MaskAllCustom(text, pii.DefaultPatterns, names), nil
}

func (s *Server) handleMaskPII(ctx context.Context, args map[string]any) (string, error) {
	return security.MaskPII(ctx, securitySvc{}, args)
}
