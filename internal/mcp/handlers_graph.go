package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/graph"
)

func (s *Server) handleAstSearch(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.AstSearch(ctx, ix, args)
}

func (s *Server) handleFrameworks(ctx context.Context, args map[string]any) (string, error) {
	return graph.Frameworks(ctx, args)
}

func (s *Server) handleFWTrace(ctx context.Context, args map[string]any) (string, error) {
	return graph.FWTrace(ctx, args)
}

func (s *Server) handleEntryPoints(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.EntryPoints(ctx, ix, args)
}

func (s *Server) handleSearch(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Search(ctx, ix, s.govContext(ctx, args, ix), args)
}

func (s *Server) handleRepoSearch(ctx context.Context, args map[string]any) (string, error) {
	return graph.RepoSearch(ctx, resolveRoot(argString(args, "root")), args)
}

func (s *Server) handleWhy(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Why(ctx, ix, args)
}

func (s *Server) handleCodeGraph(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.CodeGraph(ctx, ix, args)
}

func (s *Server) handleInherits(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Inherits(ctx, ix, args)
}

// freshnessFooter delegates to the graph leaf; the mcp wrapper stays for
// the envelope/retrieve callers.
func (s *Server) freshnessFooter(args map[string]any, ix *index.Index) string {
	return graph.FreshnessFooter(args, ix)
}

// govContext builds the kernel-hook bundle the governed graph bodies
// receive: governor construction and provenance stamping.
func (s *Server) govContext(ctx context.Context, args map[string]any, ix *index.Index) gov.GovContext {
	return gov.GovContext{
		NewGov: func() (*gov.Governor, error) {
			return s.newGovernor(ctx, args, ix)
		},
		StampGov: func(g *gov.Governor, symbols []SymbolProvenance) {
			s.stampProvenance(ctx, s.governedProvenance(ix, g.PolicySource, g.Proof, symbols))
		},
		StampRaw: func(symbols []SymbolProvenance) {
			s.stampProvenance(ctx, s.rawProvenance(ix, symbols))
		},
	}
}

func (s *Server) handleContext(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Context(ctx, ix, s.govContext(ctx, args, ix), args)
}
func (s *Server) handlePath(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Path(ctx, ix, args)
}

func (s *Server) handleCycles(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Cycles(ctx, ix, args)
}

func (s *Server) handleDead(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Dead(ctx, ix, args)
}

func (s *Server) handleLarges(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Larges(ctx, ix, args)
}

func (s *Server) handleArch(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Arch(ctx, ix, args)
}

func (s *Server) handleSurprising(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Surprising(ctx, ix, args)
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
func (s *Server) handleCommunities(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Communities(ctx, ix, s.sessionFor(argString(args, "root")), args)
}

func (s *Server) handleChurn(ctx context.Context, args map[string]any) (string, error) {
	return graph.Churn(ctx, args)
}

func (s *Server) handleNear(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Near(ctx, ix, args)
}

func (s *Server) handleGraph(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Graph(ctx, ix, s.govContext(ctx, args, ix), args)
}
func (s *Server) handleExplore(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Explore(ctx, ix, s.govContext(ctx, args, ix), args)
}
func (s *Server) handleFtsSearch(ctx context.Context, args map[string]any) (string, error) {
	return graph.FtsSearch(ctx, resolveRoot(argString(args, "root")), args)
}

func (s *Server) handleBridges(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Bridges(ctx, ix, args)
}

func (s *Server) handleCochange(ctx context.Context, args map[string]any) (string, error) {
	return graph.Cochange(ctx, args)
}

func (s *Server) handleProbe(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Probe(ctx, ix, s.govContext(ctx, args, ix), args)
}

func (s *Server) handleTrace(ctx context.Context, args map[string]any) (string, error) {
	ix, err := s.loadIndex(ctx, argString(args, "root"))
	if err != nil {
		return "", err
	}
	return graph.Trace(ctx, ix, args)
}
