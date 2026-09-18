package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/org"
)

// The org family lives in internal/mcp/org. These adapters are the
// dispatch-table surface; all logic is in the leaf.

func (s *Server) handleOrgProjects(ctx context.Context, args map[string]any) (string, error) {
	return org.Projects(ctx, args)
}

func (s *Server) handleOrgAgents(ctx context.Context, args map[string]any) (string, error) {
	return org.Agents(ctx, args)
}

func (s *Server) handleOrgTeams(ctx context.Context, args map[string]any) (string, error) {
	return org.Teams(ctx, args)
}

func (s *Server) handleOrgMemory(ctx context.Context, args map[string]any) (string, error) {
	return org.Memory(ctx, args)
}

func (s *Server) handleOrgTasks(ctx context.Context, args map[string]any) (string, error) {
	return org.Tasks(ctx, args)
}

func (s *Server) handleOrgSearch(ctx context.Context, args map[string]any) (string, error) {
	return org.Search(ctx, args)
}

func (s *Server) handleOrgAudit(ctx context.Context, args map[string]any) (string, error) {
	return org.Audit(ctx, args)
}
