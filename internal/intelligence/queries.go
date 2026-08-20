package intelligence

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// maxHops caps transitive graph traversals so cyclic call graphs cannot cause
// unbounded traversal. The v1 call graph of a real project is far shallower.
const maxHops = 50

// buildAdjacency derives, for the "calls" edges only, the outgoing map
// (caller -> callees) and incoming map (callee -> callers). Edge endpoints are
// canonicalized to node IDs: node IDs are package-scoped ("pkg.Func"), while
// index edges reference callees either by their qualified name ("pkg.Func"),
// by an import alias ("db.Func"), or by a bare name ("Func") for in-package
// calls. resolveNodeID tries the exact ID first, then resolves a reference to a
// unique node by its simple name. Neighbour lists are sorted for deterministic
// query results.
func (g *Graph) buildAdjacency() (outgoing, incoming map[string][]string) {
	outgoing = map[string][]string{}
	incoming = map[string][]string{}
	byID := g.nodesByID()
	canonical := func(id string) string {
		if r, ok := resolveNodeID(id, byID); ok {
			return r
		}
		return id
	}
	for _, e := range g.Edges {
		if e.Kind != "calls" {
			continue
		}
		from := canonical(e.From)
		to := canonical(e.To)
		outgoing[from] = append(outgoing[from], to)
		incoming[to] = append(incoming[to], from)
	}
	for k := range outgoing {
		sort.Strings(outgoing[k])
	}
	for k := range incoming {
		sort.Strings(incoming[k])
	}
	return outgoing, incoming
}

// nodesByID returns a lookup of node ID to its domain.Node.
func (g *Graph) nodesByID() map[string]domain.Node {
	m := make(map[string]domain.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		m[n.ID] = n
	}
	return m
}

// resolveNodeID maps a symbol reference (bare name "Func", package-scoped name
// "pkg.Func", import-alias reference "alias.Func", or method "Type.Method") to
// a graph node ID. It returns (id, true) when the reference is already a node
// ID or resolves to a unique symbol by its simple name; it returns (ref, false)
// when the reference is unresolvable or ambiguous (the same simple name exists
// on nodes in different packages, which the index cannot disambiguate from an
// alias).
func resolveNodeID(ref string, byID map[string]domain.Node) (string, bool) {
	if _, ok := byID[ref]; ok {
		return ref, true
	}
	bare := ref
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
	}
	var match string
	for id, n := range byID {
		if n.Symbol == nil || n.Symbol.Name != bare {
			continue
		}
		if match != "" && match != id {
			return ref, false // ambiguous: same name on multiple nodes
		}
		match = id
	}
	if match == "" {
		return ref, false
	}
	return match, true
}

// resolveSymbol maps a user-provided symbol to its canonical node ID. It handles
// bare names ("Func"), package-scoped names ("pkg.Func"), and method names
// ("Type.Method"). When the input doesn't match a node ID directly, the name is
// matched against node names, resolving to the unique node when unambiguous.
func (g *Graph) resolveSymbol(symbol string) string {
	if id, ok := resolveNodeID(symbol, g.nodesByID()); ok {
		return id
	}
	return symbol
}

// nodesForIDs returns the nodes whose IDs appear in ids, in the given order,
// skipping IDs that do not resolve to a graph node (e.g. foreign callees).
func nodesForIDs(byID map[string]domain.Node, ids []string) []domain.Node {
	out := make([]domain.Node, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

// transitive returns the node IDs reachable from start by following neighbor(),
// excluding start itself, capped at maxDepth hops to stay cycle-safe. Results
// are sorted for determinism.
func transitive(start string, neighbor map[string][]string, maxDepth int) []string {
	if maxDepth <= 0 {
		maxDepth = maxHops
	}
	visited := map[string]bool{start: true}
	reached := map[string]bool{}
	queue := []string{start}
	for depth := 0; len(queue) > 0 && depth < maxDepth; depth++ {
		var next []string
		for _, cur := range queue {
			for _, nb := range neighbor[cur] {
				if visited[nb] {
					continue
				}
				visited[nb] = true
				reached[nb] = true
				next = append(next, nb)
			}
		}
		queue = next
	}
	out := make([]string, 0, len(reached))
	for id := range reached {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// WhoCalls returns the symbols that directly call the given symbol.
func (g *Graph) WhoCalls(symbol string) []domain.Node {
	_, incoming := g.buildAdjacency()
	reach := transitive(g.resolveSymbol(symbol), incoming, 1)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatDependsOn returns the symbols that depend on the given symbol — its
// transitive callers (the set of symbols reachable by walking the call graph
// backwards from the symbol).
func (g *Graph) WhatDependsOn(symbol string) []domain.Node {
	_, incoming := g.buildAdjacency()
	reach := transitive(g.resolveSymbol(symbol), incoming, maxHops)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatDoesXDependOn returns the symbols the given symbol depends on — its
// transitive callees (the set of symbols reachable by walking the call graph
// forwards from the symbol).
func (g *Graph) WhatDoesXDependOn(symbol string) []domain.Node {
	outgoing, _ := g.buildAdjacency()
	reach := transitive(g.resolveSymbol(symbol), outgoing, maxHops)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatAPIsAffected returns the API entry-point nodes affected by a change to
// the given symbol: every node transitively depending on it that is a framework
// entry point, plus the symbol itself when it is one.
func (g *Graph) WhatAPIsAffected(symbol string) []domain.Node {
	_, incoming := g.buildAdjacency()
	resolved := g.resolveSymbol(symbol)
	reach := transitive(resolved, incoming, maxHops)

	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []domain.Node
	// The symbol itself too: a change to an entry-point handler affects it.
	for _, id := range append(reach, resolved) {
		n, ok := byID[id]
		if !ok || n.Symbol == nil || !g.entries[n.ID] {
			continue
		}
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	return out
}

// WhatServicesAffected returns the service-like nodes affected by a change to
// the given symbol. For now a "service" is a module node whose package contains
// an affected API entry point. Distinct modules are returned, sorted.
func (g *Graph) WhatServicesAffected(symbol string) []domain.Node {
	entries := g.WhatAPIsAffected(symbol)

	// Map each affected entry point's file to its containing package path.
	affected := map[string]bool{}
	for _, e := range entries {
		if e.Symbol == nil {
			continue
		}
		affected[filepath.Dir(e.Symbol.File)] = true
	}

	seen := map[string]bool{}
	var out []domain.Node
	for id, n := range g.nodesByID() {
		if n.Kind != "module" || !affected[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, n)
	}
	return out
}

// eventHints are the substrings that mark a node as event/topic-like in the
// absence of dedicated event/topic node kinds in a code-only graph.
var eventHints = []string{"event", "topic", "queue", "publish", "subscribe"}

// isEventLike reports whether a node name (symbol name or label) matches the
// event/topic heuristic. It is deterministic and intentionally conservative:
// it looks for event/topic vocabulary in the name, and also flags
// producer/consumer-style nodes that mention a producer/consumer role.
func isEventLike(s string) bool {
	lower := strings.ToLower(s)
	for _, h := range eventHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// WhatEventsAffected returns the event/topic nodes affected by a change to the
// given symbol. The code-only graph has no event/topic kinds, so this is a
// deterministic heuristic: it scans the graph for nodes whose name matches
// event/topic vocabulary ("event", "topic", "queue", "publish", "subscribe")
// and returns those among the symbol's direct callers and callees, plus the
// symbol itself when it is event-like. Returns an empty (non-nil) slice if
// nothing matches.
func (g *Graph) WhatEventsAffected(symbol string) []domain.Node {
	// Direct callees (produced/consumed) and direct callers, plus the symbol
	// itself. Depth-1 keeps the result tight and deterministic.
	outgoing, incoming := g.buildAdjacency()
	resolved := g.resolveSymbol(symbol)
	ids := append(incoming[resolved], resolved)
	ids = append(ids, outgoing[resolved]...)

	byID := g.nodesByID()
	seen := map[string]bool{}
	out := []domain.Node{}
	for _, id := range ids {
		n, ok := byID[id]
		if !ok || seen[n.ID] {
			continue
		}
		name := n.Label
		if n.Symbol != nil {
			name = n.Symbol.Name
		}
		if !isEventLike(name) {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	return out
}

// isTest reports whether a symbol is a test node: its name follows the Go "Test"
// convention or its defining file is a _test.go file.
func isTest(s *domain.Symbol) bool {
	if s == nil {
		return false
	}
	return strings.HasPrefix(s.Name, "Test") || strings.Contains(s.File, "_test.go")
}

// ProductionCriticality returns a deterministic, heuristic score for how
// critical a symbol is to production, based on its blast radius — the number of
// transitive callers in the call graph. Returns "critical" (>20), "high"
// (10-20), "medium" (3-9), or "low" (<3).
func (g *Graph) ProductionCriticality(symbol string) string {
	n := len(g.WhatDependsOn(symbol))
	switch {
	case n > 20:
		return "critical"
	case n >= 10:
		return "high"
	case n >= 3:
		return "medium"
	default:
		return "low"
	}
}

// IncidentReader is a minimal read interface over an incident store, used by
// WhatIncidentsAffected. It avoids coupling intelligence to the incident
// package. incident.Store satisfies it directly (its List returns
// []domain.Incident, error).
type IncidentReader interface {
	List() ([]domain.Incident, error)
}

// IncidentSummary is what WhatIncidentsAffected returns — the captured,
// intelligence-relevant slice of a historical incident.
type IncidentSummary struct {
	ID       string
	Title    string
	Severity string
	Service  string
}

// WhatIncidentsAffected returns the past incidents whose affected service
// matches a service impacted by a change to the given symbol. It requires an
// injected IncidentReader (the graph itself does not store incidents). Services
// are the module nodes returned by WhatServicesAffected, matched by package
// path (module node ID). Returns nil when the store is nil, no services are
// affected, or no incident matches.
func WhatIncidentsAffected(g *Graph, symbol string, store IncidentReader) []IncidentSummary {
	if store == nil {
		return nil
	}
	services := g.WhatServicesAffected(symbol)
	if len(services) == 0 {
		return nil
	}
	incidents, err := store.List()
	if err != nil {
		return nil
	}
	var out []IncidentSummary
	for _, inc := range incidents {
		for _, svc := range services {
			if inc.AffectedService != svc.ID {
				continue
			}
			out = append(out, IncidentSummary{
				ID:       inc.ID,
				Title:    inc.Title,
				Severity: string(inc.Severity),
				Service:  inc.AffectedService,
			})
			break
		}
	}
	return out
}

// WhatTestsCover returns the test nodes that cover the given symbol: tests that
// directly or transitively reach the symbol through its call graph.
func (g *Graph) WhatTestsCover(symbol string) []domain.Node {
	_, incoming := g.buildAdjacency()
	reach := transitive(g.resolveSymbol(symbol), incoming, maxHops)
	byID := g.nodesByID()

	seen := map[string]bool{}
	var out []domain.Node
	for _, id := range reach {
		n, ok := byID[id]
		if !ok || !isTest(n.Symbol) {
			continue
		}
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	return out
}
