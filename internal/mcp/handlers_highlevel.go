// Highlevel tool adapters — thin root wrappers around the highlevel leaf
// package. The 16 high-level control-plane tool bodies (kern_analyze,
// kern_plan, kern_execute, kern_verify, kern_incident, kern_what_if,
// kern_impact, kern_agents, kern_loop, kern_run, kern_workflow,
// kern_correlate, kern_learn, kern_modernize, kern_audit, kern_approve) live
// in internal/mcp/highlevel as plain functions; this file wires them into
// *Server: each handler builds the leaf Hooks from server state (platformFor,
// governance) and delegates. Handler method names and signatures are
// unchanged, so dispatch, catalog and parity tests compile untouched.
package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/highlevel"
)

// highlevelHooks wires the server's platform resolver and the governance
// package into the highlevel leaf. All 16 handlers share this bundle:
// PlatformFor backs the TaskService routings, Audit and PendingApprovals back
// the kern_audit / kern_approve surfaces.
func (s *Server) highlevelHooks() highlevel.Hooks {
	return highlevel.Hooks{
		PlatformFor: s.platformFor,
		Audit: func(ctx context.Context, root string) ([]governance.AuditEntry, error) {
			return governance.ReadAuditTrail(ctx, root, "")
		},
		PendingApprovals: func(ctx context.Context, root string) ([]domain.Approval, error) {
			return governance.NewFileStore(root).Pending()
		},
	}
}

func (s *Server) handleAnalyze(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Analyze(ctx, s.highlevelHooks(), args)
}

func (s *Server) handlePlan(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Plan(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleExecute(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Execute(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleVerify(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Verify(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleIncident(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Incident(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleWhatIf(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.WhatIf(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleImpact(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Impact(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleAgents(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Agents(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleLoop(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Loop(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleRun(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Run(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleWorkflow(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Workflow(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleCorrelate(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Correlate(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleLearn(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Learn(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleModernize(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Modernize(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleAudit(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Audit(ctx, s.highlevelHooks(), args)
}

func (s *Server) handleApprove(ctx context.Context, args map[string]any) (string, error) {
	return highlevel.Approve(ctx, s.highlevelHooks(), args)
}

// HandleMeta is the exported wrapper around handleMeta, used by the `kern meta`
// CLI subcommand so CLI and MCP use the same classifier and dispatch path.
func (s *Server) HandleMeta(ctx context.Context, args map[string]any) (string, error) {
	return s.handleMeta(ctx, args)
}
