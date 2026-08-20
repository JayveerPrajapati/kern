// Package twin provides extractors that build the non-code dimensions of
// the Digital Twin (APIs, databases, messaging, infrastructure, runtime)
// and a Merge function that unifies them with the code graph.
package twin

import (
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/twin/api"
	"github.com/JayveerPrajapati/kern/internal/twin/data"
	"github.com/JayveerPrajapati/kern/internal/twin/infra"
	"github.com/JayveerPrajapati/kern/internal/twin/messaging"
	twinruntime "github.com/JayveerPrajapati/kern/internal/twin/runtime"
)

// Extractors bundles all twin extractors for convenient invocation.
type Extractors struct {
	API       *api.Extractor
	Data      *data.Extractor
	Messaging *messaging.Extractor
	Infra     *infra.Extractor
	Runtime   *twinruntime.Builder // nil when no runtime source
}

// NewExtractors creates all extractors for the given root. The runtime
// builder is nil when source is nil (no production telemetry).
func NewExtractors(root string, source runtime.Source) *Extractors {
	e := &Extractors{
		API:       api.New(root),
		Data:      data.New(root),
		Messaging: messaging.New(root),
		Infra:     infra.New(root),
	}
	if source != nil {
		e.Runtime = twinruntime.New(source)
	}
	return e
}

// ExtractAll runs all extractors and returns the combined nodes and edges.
// Errors from individual extractors are returned but do not stop the others.
func (e *Extractors) ExtractAll() ([]domain.Node, []domain.Edge, []error) {
	var allNodes []domain.Node
	var allEdges []domain.Edge
	var errs []error

	if e.API != nil {
		nodes, edges, err := e.API.Extract()
		allNodes, allEdges = append(allNodes, nodes...), append(allEdges, edges...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if e.Data != nil {
		nodes, edges, err := e.Data.Extract()
		allNodes, allEdges = append(allNodes, nodes...), append(allEdges, edges...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if e.Messaging != nil {
		nodes, edges, err := e.Messaging.Extract()
		allNodes, allEdges = append(allNodes, nodes...), append(allEdges, edges...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if e.Infra != nil {
		nodes, edges, err := e.Infra.Extract()
		allNodes, allEdges = append(allNodes, nodes...), append(allEdges, edges...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if e.Runtime != nil {
		nodes, edges, err := e.Runtime.Build()
		allNodes, allEdges = append(allNodes, nodes...), append(allEdges, edges...)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return dedupNodes(allNodes), dedupEdges(allEdges), errs
}

// dedupNodes removes duplicate nodes by ID (keeping the first occurrence);
// different extractors may produce the same node.
func dedupNodes(nodes []domain.Node) []domain.Node {
	seen := map[string]bool{}
	result := nodes[:0]
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		result = append(result, n)
	}
	return result
}

// edgeKey builds a collision-safe dedup key for an edge from its From, To, and
// Kind. "\x00" is used as the separator because it cannot appear in a node ID
// (node IDs are built from printable source text), so no two distinct
// (From, To, Kind) triples ever share a key.
func edgeKey(e domain.Edge) string {
	return e.From + "\x00" + e.To + "\x00" + e.Kind
}

// dedupEdges removes duplicate edges by (From, To, Kind), keeping the first
// occurrence. This keeps repeated ExtractAll calls from multiplying identical
// edges across the combined output.
func dedupEdges(edges []domain.Edge) []domain.Edge {
	seen := map[string]bool{}
	result := edges[:0]
	for _, e := range edges {
		k := edgeKey(e)
		if seen[k] {
			continue
		}
		seen[k] = true
		result = append(result, e)
	}
	return result
}

// Merge combines twin extractor output into an existing intelligence.Graph,
// deduplicating nodes by ID. This is what makes the graph a true knowledge
// graph (code + API + data + messaging + infra + runtime).
func Merge(g *intelligence.Graph, ext *Extractors) error {
	if ext == nil {
		return nil
	}
	nodes, edges, errs := ext.ExtractAll()
	// Merge nodes (dedup against existing).
	existing := map[string]bool{}
	for _, n := range g.Nodes {
		existing[n.ID] = true
	}
	for _, n := range nodes {
		if !existing[n.ID] {
			g.Nodes = append(g.Nodes, n)
			existing[n.ID] = true
		}
	}
	// Merge edges, deduplicating by (From, To, Kind) so repeated merges are
	// idempotent rather than multiplying every edge on each call.
	existingEdges := map[string]bool{}
	for _, e := range g.Edges {
		existingEdges[edgeKey(e)] = true
	}
	for _, e := range dedupEdges(edges) {
		k := edgeKey(e)
		if existingEdges[k] {
			continue
		}
		existingEdges[k] = true
		g.Edges = append(g.Edges, e)
	}
	// Non-fatal: return the first extractor error if any.
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// MergeIntoGraph builds a graph from an index, runs all twin extractors, and
// returns the merged knowledge graph. This is the one-call way to get the full
// knowledge graph (code + API + data + messaging + infra + runtime).
func MergeIntoGraph(root string, source runtime.Source) (intelligence.Graph, error) {
	ix, err := index.Build(root)
	if err != nil {
		return intelligence.Graph{}, err
	}
	g := intelligence.FromIndex(ix)
	ext := NewExtractors(root, source)
	// Non-fatal — return the graph with whatever merged.
	_ = Merge(&g, ext)
	return g, nil
}
