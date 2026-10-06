package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/security"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/secscan"
)

// securitySvc implements security.SecurityService directly over the internal
// engines — the thin facade the dissolved internal/service layer used to
// provide, kept local to the MCP adapter.
type securitySvc struct{}

func (securitySvc) Scan(ctx context.Context, root string) ([]secscan.Finding, error) {
	return secscan.Scan(root)
}

func (securitySvc) FilterBySeverity(findings []secscan.Finding, allow []string) []secscan.Finding {
	return secscan.FilterBySeverity(findings, allow)
}

func (securitySvc) Render(findings []secscan.Finding, max int) string {
	return secscan.Render(findings, max)
}

func (securitySvc) Mask(ctx context.Context, text string, names []string) (pii.Result, error) {
	return pii.MaskAllCustom(text, pii.DefaultPatterns, names), nil
}

func (s *Server) securityHooks() security.Hooks {
	return security.Hooks{
		LoadIndex:      s.loadIndex,
		ChangedContext: s.changedContext,
		SecuritySvc:    securitySvc{},
		ServerVersion:  currentServerVersion(),
	}
}

func (s *Server) handleMaskPII(ctx context.Context, args map[string]any) (string, error) {
	return security.MaskPII(ctx, securitySvc{}, args)
}

func (s *Server) handleSecurity(ctx context.Context, args map[string]any) (string, error) {
	return security.Scan(ctx, securitySvc{}, args)
}

func (s *Server) handleTaint(ctx context.Context, args map[string]any) (string, error) {
	return security.Taint(ctx, s.securityHooks(), args)
}

func (s *Server) handleSafeDelete(ctx context.Context, args map[string]any) (string, error) {
	return security.SafeDelete(ctx, s.securityHooks(), args)
}

func (s *Server) handleSchemaValidate(ctx context.Context, args map[string]any) (string, error) {
	return security.SchemaValidate(ctx, args)
}

func (s *Server) handleVerifyOutput(ctx context.Context, args map[string]any) (string, error) {
	return security.VerifyOutput(ctx, s.securityHooks(), args)
}

func (s *Server) handleCheckDraft(ctx context.Context, args map[string]any) (string, error) {
	return security.CheckDraft(ctx, s.securityHooks(), args)
}

func (s *Server) handleGuardCheck(ctx context.Context, args map[string]any) (string, error) {
	return security.GuardCheck(ctx, s.securityHooks(), args)
}
