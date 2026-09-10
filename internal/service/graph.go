package service

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// GraphService centralizes code-graph operations: exploring a symbol's
// neighbourhood, searching the index, computing call paths, and explaining
// why a symbol exists.
type GraphService interface {
	// Explore returns a symbol's definition, source, call flow and blast
	// radius in one report (kern explore).
	Explore(ctx context.Context, root, symbol string, depth, maxNodes int) (*intel.ExploreReport, error)
	// Search runs a ranked free-text search over the index (kern search).
	Search(ctx context.Context, root, query string, limit int) ([]index.Symbol, error)
	// AstSearch runs a pattern-based symbol search (kern ast).
	AstSearch(ctx context.Context, root, pattern string, limit int) ([]index.Symbol, error)
	// Path returns the shortest call path between two symbols (kern path).
	Path(ctx context.Context, root, from, to string) ([]string, error)
	// Why explains a symbol's rationale and dependents (kern why).
	Why(ctx context.Context, root, symbol string) (*intel.WhyInfo, error)
	// Graph renders the neighbourhood of a symbol: definition, callers, and
	// what it calls (kern graph, text form).
	Graph(ctx context.Context, root, symbol string) (string, error)
	// Neighborhood returns the structured graph neighbourhood of a symbol
	// (kern graph --json / --graphml / --html).
	Neighborhood(ctx context.Context, root, symbol string) (index.GraphResult, error)
}

// graphService is the default GraphService implementation backed by the
// internal/index and internal/intel engines.
type graphService struct{}

func newGraphService() *graphService { return &graphService{} }

func (s *graphService) index(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return index.LoadOrBuild(resolveRoot(root))
}

func (s *graphService) Explore(ctx context.Context, root, symbol string, depth, maxNodes int) (*intel.ExploreReport, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return nil, err
	}
	return intel.Explore(ix, symbol, depth, maxNodes)
}

func (s *graphService) Search(ctx context.Context, root, query string, limit int) ([]index.Symbol, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	return intel.RankedSearch(ix, query, limit), nil
}

func (s *graphService) AstSearch(ctx context.Context, root, pattern string, limit int) ([]index.Symbol, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	return ix.Search(pattern, limit), nil
}

func (s *graphService) Path(ctx context.Context, root, from, to string) ([]string, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return nil, err
	}
	f, okFrom := intel.Resolve(ix, from)
	if !okFrom {
		return nil, fmt.Errorf("unknown symbol: %s", from)
	}
	t, okTo := intel.Resolve(ix, to)
	if !okTo {
		return nil, fmt.Errorf("unknown symbol: %s", to)
	}
	return intel.ShortestPath(ix, f, t), nil
}

func (s *graphService) Why(ctx context.Context, root, symbol string) (*intel.WhyInfo, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return nil, err
	}
	info, ok := intel.Why(ix, symbol)
	if !ok {
		return nil, fmt.Errorf("no symbol found: %s", symbol)
	}
	return &info, nil
}

func (s *graphService) Graph(ctx context.Context, root, symbol string) (string, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return "", err
	}
	return ix.Graph(symbol), nil
}

func (s *graphService) Neighborhood(ctx context.Context, root, symbol string) (index.GraphResult, error) {
	ix, err := s.index(ctx, root)
	if err != nil {
		return index.GraphResult{}, err
	}
	g, ok := ix.Neighborhood(symbol)
	if !ok {
		return index.GraphResult{}, fmt.Errorf("no symbol found: %s", symbol)
	}
	return g, nil
}
