package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	mcpgovernance "github.com/JayveerPrajapati/kern/internal/mcp/governance"
)

// The governance-family handler bodies (kern_lock, kern_unlock,
// kern_lock_status, kern_usage_guide, kern_rename, kern_authorize_context)
// live in internal/mcp/governance. These adapters are the dispatch-table
// surface; they build the leaf Hooks from server state and delegate.

func (s *Server) governanceHooks() mcpgovernance.Hooks {
	return mcpgovernance.Hooks{
		Mu:        &s.mu,
		Locks:     s.locks,
		LoadIndex: s.loadIndex,
		Guide:     Guide,
		StampGoverned: func(ctx context.Context, ix *index.Index, policySource string, proof governance.AuthorizationProof, syms []SymbolProvenance) {
			s.stampProvenance(ctx, s.governedProvenance(ix, policySource, proof, syms))
		},
	}
}

func (s *Server) handleLock(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.Lock(ctx, s.governanceHooks(), args)
}

func (s *Server) handleUnlock(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.Unlock(ctx, s.governanceHooks(), args)
}

func (s *Server) handleLockStatus(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.LockStatus(ctx, s.governanceHooks(), args)
}

func (s *Server) handleUsageGuide(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.UsageGuide(ctx, s.governanceHooks(), args)
}

func (s *Server) handleRename(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.Rename(ctx, s.governanceHooks(), args)
}

func (s *Server) handleAuthorizeContext(ctx context.Context, args map[string]any) (string, error) {
	return mcpgovernance.AuthorizeContext(ctx, s.governanceHooks(), args)
}
