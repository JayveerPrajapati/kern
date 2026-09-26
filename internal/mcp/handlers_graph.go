// Graph-family tool adapters — thin root wrappers around the graph leaf
// package. The graph-family tool bodies (kern_ast_search, kern_frameworks,
// kern_fw_trace, kern_entry_points, kern_search, kern_repo_search, kern_why,
// kern_inherits, kern_context, kern_path, kern_cycles, kern_dead, kern_larges,
// kern_arch, kern_surprising, kern_snapshot, kern_communities, kern_churn,
// kern_near, kern_graph, kern_explore, kern_fts_search, kern_bridges,
// kern_cochange, kern_probe, kern_trace) live in internal/mcp/graph as plain
// functions. The governed bodies receive their GovContext hook bundle from
// gov.NewGovContext (the bundle factory lives in the gov leaf); this adapter
// only injects the two core closures — the governor factory and the
// provenance stamp. Handler method names and signatures are unchanged, so
// dispatch, catalog and parity tests compile untouched.
package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/graph"
)

// graphCall delegates an index-based graph call: it loads the index (the
// loadIndex+err dance every handler did before the extraction) and forwards
// to the leaf body.
func (s *Server) graphCall(ctx context.Context, args map[string]any, fn func(context.Context, *index.Index, map[string]any) (string, error)) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return fn(ctx, ix, args)
}

// graphGoverned delegates a governed graph call: it loads the index and
// builds the per-call GovContext bundle from the gov-leaf factory.
func (s *Server) graphGoverned(ctx context.Context, args map[string]any, fn func(context.Context, *index.Index, gov.GovContext, map[string]any) (string, error)) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return fn(ctx, ix, s.govContext(ctx, args, ix), args)
}

// govContext builds the kernel-hook bundle governed graph bodies receive.
// The bundle construction lives in gov.NewGovContext; the adapter supplies
// only the governor factory (each call re-authorizes via gov.New) and the
// provenance stamp (writes to the per-call scope, session/audit). Retrieve
// adapters share this hook.
func (s *Server) govContext(ctx context.Context, args map[string]any, ix *index.Index) gov.GovContext {
	return gov.NewGovContext(ix, s.commit, func() (*gov.Governor, error) {
		return s.newGovernor(ctx, args, ix)
	}, func(p *Provenance) {
		s.stampProvenance(ctx, p)
	})
}

// freshnessFooter delegates to the graph leaf; the mcp wrapper stays for
// the envelope/retrieve callers.
func (s *Server) freshnessFooter(args map[string]any, ix *index.Index) string {
	return graph.FreshnessFooter(args, ix)
}

func (s *Server) handleAstSearch(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.AstSearch)
}
func (s *Server) handleEntryPoints(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.EntryPoints)
}
func (s *Server) handleWhy(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Why)
}
func (s *Server) handleInherits(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Inherits)
}
func (s *Server) handlePath(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Path)
}
func (s *Server) handleCycles(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Cycles)
}
func (s *Server) handleDead(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Dead)
}
func (s *Server) handleLarges(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Larges)
}
func (s *Server) handleArch(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Arch)
}
func (s *Server) handleSurprising(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Surprising)
}
func (s *Server) handleNear(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Near)
}
func (s *Server) handleBridges(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Bridges)
}
func (s *Server) handleTrace(ctx context.Context, args map[string]any) (string, error) {
	return s.graphCall(ctx, args, graph.Trace)
}

func (s *Server) handleSearch(ctx context.Context, args map[string]any) (string, error) {
	return s.graphGoverned(ctx, args, graph.Search)
}
func (s *Server) handleContext(ctx context.Context, args map[string]any) (string, error) {
	return s.graphGoverned(ctx, args, graph.Context)
}
func (s *Server) handleGraph(ctx context.Context, args map[string]any) (string, error) {
	return s.graphGoverned(ctx, args, graph.Graph)
}
func (s *Server) handleExplore(ctx context.Context, args map[string]any) (string, error) {
	return s.graphGoverned(ctx, args, graph.Explore)
}
func (s *Server) handleProbe(ctx context.Context, args map[string]any) (string, error) {
	return s.graphGoverned(ctx, args, graph.Probe)
}

func (s *Server) handleFrameworks(ctx context.Context, args map[string]any) (string, error) {
	return graph.Frameworks(ctx, args)
}
func (s *Server) handleFWTrace(ctx context.Context, args map[string]any) (string, error) {
	return graph.FWTrace(ctx, args)
}
func (s *Server) handleRepoSearch(ctx context.Context, args map[string]any) (string, error) {
	return graph.RepoSearch(ctx, resolveRoot(argString(args, "root")), args)
}
func (s *Server) handleChurn(ctx context.Context, args map[string]any) (string, error) {
	return graph.Churn(ctx, args)
}
func (s *Server) handleFtsSearch(ctx context.Context, args map[string]any) (string, error) {
	return graph.FtsSearch(ctx, resolveRoot(argString(args, "root")), args)
}
func (s *Server) handleCochange(ctx context.Context, args map[string]any) (string, error) {
	return graph.Cochange(ctx, args)
}

func (s *Server) handleCommunities(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Communities(ctx, ix, s.sessionFor(argString(args, "root")), args)
}

func (s *Server) handleSnapshot(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	switch argString(args, "action") {
	case "create":
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			return "", err
		}
		return graph.SnapshotCreate(ix, args)
	case "verify":
		return graph.SnapshotVerify(resolveRoot(root), args)
	default:
		return "", fmt.Errorf("snapshot: action %q not supported (want \"create\" or \"verify\")", argString(args, "action"))
	}
}
