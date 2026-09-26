package mcp

import (
	"context"

	mcpcontext "github.com/JayveerPrajapati/kern/internal/mcp/context"
)

// contextHooks wires the server state the context-family handlers need: the
// workspace roots (kern_compact_file resolves bare absolute paths against
// them) and the session index loader (kern_onboard refreshes the project
// index). The other handlers (buddy, project_map, pack, fit_context) are
// Server-independent.
func (s *Server) contextHooks() mcpcontext.Hooks {
	return mcpcontext.Hooks{
		LoadIndex: s.loadIndex,
		Roots:     func() []string { return s.roots },
	}
}

func (s *Server) handleCompact(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.Compact(ctx, s.contextHooks(), args)
}

func (s *Server) handleBuddy(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.Buddy(ctx, args)
}

func (s *Server) handleOnboard(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.Onboard(ctx, s.contextHooks(), args)
}

func (s *Server) handleProjectMap(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.ProjectMap(ctx, args)
}

func (s *Server) handlePack(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.Pack(ctx, args)
}

func (s *Server) handleFitContext(ctx context.Context, args map[string]any) (string, error) {
	return mcpcontext.FitContext(ctx, args)
}
