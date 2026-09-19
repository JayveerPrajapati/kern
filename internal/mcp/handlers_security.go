package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/security"
)

func (s *Server) securityHooks() security.Hooks {
	return security.Hooks{
		LoadIndex:      s.loadIndex,
		ChangedContext: s.changedContext,
		SecuritySvc:    s.svc.Security,
		ServerVersion:  serverVersion,
	}
}

func (s *Server) handleMaskPII(ctx context.Context, args map[string]any) (string, error) {
	return security.MaskPII(ctx, s.svc.Security, args)
}

func (s *Server) handleSecurity(ctx context.Context, args map[string]any) (string, error) {
	return security.Scan(ctx, s.svc.Security, args)
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
