package intel

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// maxHops caps transitive graph traversals so cyclic call graphs cannot cause
// unbounded traversal. The v1 call graph of a real project is far shallower.
const maxHops = 50

// adjacency returns the cached outgoing/incoming "calls" adjacency for the
// requested precision mode, building each variant once per graph. Callers
// must treat the returned maps as read-only; the one call site that
// extends a slice copies it first.
func (g *Graph) adjacency(strict bool) (outgoing, incoming map[string][]string) {
	if strict {
		g.adjStrictOnce.Do(func() {
			g.adjStrictOut, g.adjStrictIn = g.buildAdjacencyOpt(true)
		})
		return g.adjStrictOut, g.adjStrictIn
	}
	g.adjLooseOnce.Do(func() {
		g.adjLooseOut, g.adjLooseIn = g.buildAdjacencyOpt(false)
	})
	return g.adjLooseOut, g.adjLooseIn
}

// buildAdjacencyOpt builds the adjacency map with a precision mode. When
// strict is true, "calls" edges whose caller node's language is not
// "resolved"-precision (per the index's PrecisionByLang) are dropped, so
// strict consumers report those callers as unknown instead of trusting
// heuristic cross-file guesses.
func (g *Graph) buildAdjacencyOpt(strict bool) (outgoing, incoming map[string][]string) {
	outgoing = map[string][]string{}
	incoming = map[string][]string{}
	g.initIndex()
	langByID := map[string]string{}
	if strict {
		for _, n := range g.Nodes {
			if n.Symbol != nil && n.Symbol.Language != "" {
				langByID[n.ID] = n.Symbol.Language
			}
		}
	}
	canonical := func(id string) (string, bool) {
		if r, ok := g.resolveNodeID(id); ok {
			return r, true
		}
		return id, false
	}
	for _, e := range g.Edges {
		if e.Kind != "calls" {
			continue
		}
		from, _ := canonical(e.From)
		if strict {
			if p := g.precisionByLang[langByID[from]]; p != "resolved" {
				continue
			}
		}
		to, ok := canonical(e.To)
		if !ok {
			// Cross-package callee recorded as an import-qualified
			// reference ("index.Load") whose simple name alone is
			// ambiguous. Link it via the caller's package imports so the
			// edge is not silently dropped from blast-radius traversals.
			if rid, linked := g.resolveImportQualified(e.To, from); linked {
				to = rid
			}
		}
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

// initIndex lazily builds the node lookup and name index once, on first use.
// The graph is read-only after construction (FromIndex/NewWithGraph), so the
// caches never go stale during the graph's lifetime.
func (g *Graph) initIndex() {
	g.indexOnce.Do(func() {
		g.byID = make(map[string]domain.Node, len(g.Nodes))
		g.nameIndex = make(map[string][]string)
		for _, n := range g.Nodes {
			g.byID[n.ID] = n
			if n.Symbol != nil {
				g.nameIndex[n.Symbol.Name] = append(g.nameIndex[n.Symbol.Name], n.ID)
			}
		}
		g.nodePkg = make(map[string]string)
		g.pkgImports = make(map[string][]string)
		for _, n := range g.Nodes {
			if n.Symbol == nil || n.Symbol.Qualified == "" {
				continue
			}
			// Node IDs are "<pkg>.<Qualified>" (package-scoped); stripping the
			// known symbol suffix recovers the package path. A node whose ID
			// equals its Qualified is a root-package symbol (no prefix) and
			// gets no package entry — import linking simply never applies.
			if pkg := strings.TrimSuffix(n.ID, "."+n.Symbol.Qualified); pkg != n.ID {
				g.nodePkg[n.ID] = pkg
			}
		}
		for _, e := range g.Edges {
			if e.Kind == "imports" {
				g.pkgImports[e.From] = append(g.pkgImports[e.From], e.To)
			}
		}
	})
}

// nodesByID returns a lookup of node ID to its domain.Node. The result is built
// once and cached on the Graph, so repeated calls do not rebuild the map.
func (g *Graph) nodesByID() map[string]domain.Node {
	g.initIndex()
	return g.byID
}

// NodeByID returns the graph node with the given node ID. It is the exported
// form of the cached byID lookup (O(1), built once per graph) so callers
// outside the package — e.g. the web console's /v1/graph handler — can fetch
// a node without a linear scan of g.Nodes. The returned node is a copy; the
// graph stays read-only after construction.
func (g *Graph) NodeByID(id string) (domain.Node, bool) {
	g.initIndex()
	n, ok := g.byID[id]
	return n, ok
}

// resolveNodeID maps a symbol reference (bare name "Func", package-scoped name
// "pkg.Func", import-alias reference "alias.Func", or method "Type.Method") to
// a graph node ID. It returns (id, true) when the reference is already a node
// ID or resolves to a unique symbol by its simple name; it returns (ref, false)
// when the reference is unresolvable or ambiguous (the same simple name exists
// on nodes in different packages, which the index cannot disambiguate from an
// alias).
func (g *Graph) resolveNodeID(ref string) (string, bool) {
	g.initIndex()
	if _, ok := g.byID[ref]; ok {
		return ref, true
	}
	bare := ref
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
	}
	switch ids := g.nameIndex[bare]; len(ids) {
	case 0:
		return ref, false // unresolvable
	case 1:
		return ids[0], true // unique simple name
	default:
		// Ambiguous simple name: prefer an exact qualified match, else a
		// receiver match for "Type.Method" references, so overloaded or
		// package-qualified methods do not resolve to an arbitrary node.
		qualifier := ""
		if i := strings.LastIndexByte(ref, '.'); i >= 0 {
			qualifier = ref[:i]
		}
		var suffix, nested string
		var haveSuffix, haveNested bool
		for _, id := range ids {
			n, ok := g.byID[id]
			if !ok || n.Symbol == nil {
				continue
			}
			if n.Symbol.Qualified == ref {
				return id, true
			}
			if qualifier != "" && n.Symbol.Receiver != "" {
				switch {
				case n.Symbol.Receiver == qualifier:
					return id, true
				case strings.HasSuffix(n.Symbol.Receiver, "."+qualifier):
					if !haveSuffix {
						suffix, haveSuffix = id, true
					}
				case booleanNested(qualifier, n.Symbol.Receiver):
					if !haveNested {
						nested, haveNested = id, true
					}
				}
			}
		}
		if haveSuffix {
			return suffix, true
		}
		if haveNested {
			return nested, true
		}
		return ref, false // ambiguous: same name on multiple nodes
	}
}

// Resolvable reports whether ref maps to a node in the graph (a bare symbol
// name, package-scoped name, or method name that uniquely resolves). Used to
// verify natural-language extraction against the real index.
func (g *Graph) Resolvable(ref string) bool {
	_, ok := g.resolveNodeID(ref)
	return ok
}

// ResolveNodeID maps a user-provided entity reference to its canonical node ID
// (bare name, package-scoped name, or method name). It is the exported form of
// resolveNodeID so web handlers can look up graph entities by symbol name
// without building a fresh index.
func (g *Graph) ResolveNodeID(ref string) (string, bool) {
	return g.resolveNodeID(ref)
}

// resolveSymbol maps a user-provided symbol to its canonical node ID. It handles
// bare names ("Func"), package-scoped names ("pkg.Func"), and method names
// ("Type.Method"). When the input doesn't match a node ID directly, the name is
// matched against node names, resolving to the unique node when unambiguous.
func (g *Graph) resolveSymbol(symbol string) string {
	if id, ok := g.resolveNodeID(symbol); ok {
		return id
	}
	return symbol
}

// resolveImportQualified links a qualified callee reference that
// resolveNodeID cannot match (the simple name exists in several packages)
// to the unique same-named symbol in a package the CALLING symbol's package
// imports, when the reference's qualifier matches that import's final path
// segment. This mirrors how Go resolves "index.Load" in source, so it
// restores cross-package edges the plain resolver drops without ever
// forging a link: a qualifier that matches no import (a local variable or
// type receiver, e.g. "info.Name" or "Builder.String") stays unresolved, as
// does a qualifier matching multiple candidate packages.
func (g *Graph) resolveImportQualified(ref, callerID string) (string, bool) {
	g.initIndex()
	i := strings.LastIndexByte(ref, '.')
	if i <= 0 || i >= len(ref)-1 {
		return "", false
	}
	qual, name := ref[:i], ref[i+1:]
	// Package qualifiers are conventionally lowercase; an uppercase
	// qualifier is almost always a type or variable receiver, which the
	// caller's imports cannot disambiguate.
	if r, _ := utf8.DecodeRuneInString(qual); unicode.IsUpper(r) {
		return "", false
	}
	callerPkg, ok := g.nodePkg[callerID]
	if !ok || callerPkg == "" {
		return "", false
	}
	imports := g.pkgImports[callerPkg]
	if len(imports) == 0 {
		return "", false
	}
	var found string
	for _, id := range g.nameIndex[name] {
		localPkg := g.nodePkg[id]
		if localPkg == "" {
			continue
		}
		if importMatchesQualifier(imports, qual, localPkg) {
			if found != "" {
				return "", false // ambiguous across imported packages
			}
			found = id
		}
	}
	return found, found != ""
}

// importMatchesQualifier reports whether localPkg is the package an import
// with final path segment qual refers to: the import path equals the local
// package path or ends with it (the graph keys packages by repo-relative
// path while imports are recorded as full import strings).
func importMatchesQualifier(imports []string, qual, localPkg string) bool {
	for _, imp := range imports {
		imp = strings.Trim(imp, `"' `)
		if imp == "" {
			continue
		}
		seg := imp[strings.LastIndexByte(imp, '/')+1:]
		if seg != qual {
			continue
		}
		if imp == localPkg || strings.HasSuffix(imp, "/"+localPkg) {
			return true
		}
	}
	return false
}

// ResolveEdgeEndpoint resolves a raw "calls" edge endpoint as recorded by
// the index (bare name, qualified name, or import-alias form) to its
// canonical node ID, so raw-edge consumers (the context engine, what-if)
// no longer drop cross-package callees whose simple name is ambiguous.
// callerRef is the endpoint on the other side of the edge (the caller for
// a To endpoint); its package imports provide the disambiguation context.
func (g *Graph) ResolveEdgeEndpoint(ref, callerRef string) (string, bool) {
	if id, ok := g.resolveNodeID(ref); ok {
		return id, true
	}
	callerID, ok := g.resolveNodeID(callerRef)
	if !ok {
		return "", false
	}
	return g.resolveImportQualified(ref, callerID)
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
	return g.WhoCallsPrecise(symbol, false)
}

// WhoCallsPrecise is WhoCalls with a precision mode: when strict is true,
// direct callers whose caller language is not "resolved"-precision are
// skipped (reported as unknown rather than guessed).
func (g *Graph) WhoCallsPrecise(symbol string, strict bool) []domain.Node {
	_, incoming := g.adjacency(strict)
	reach := transitive(g.resolveSymbol(symbol), incoming, 1)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatDependsOn returns the symbols that depend on the given symbol — its
// transitive callers (the set of symbols reachable by walking the call graph
// backwards from the symbol).
func (g *Graph) WhatDependsOn(symbol string) []domain.Node {
	return g.WhatDependsOnPrecise(symbol, false)
}

// WhatDependsOnPrecise is WhatDependsOn with a precision mode: when strict is
// true, callers whose caller language is not "resolved"-precision are skipped.
func (g *Graph) WhatDependsOnPrecise(symbol string, strict bool) []domain.Node {
	_, incoming := g.adjacency(strict)
	reach := transitive(g.resolveSymbol(symbol), incoming, maxHops)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatDoesXDependOn returns the symbols the given symbol depends on — its
// transitive callees (the set of symbols reachable by walking the call graph
// forwards from the symbol).
func (g *Graph) WhatDoesXDependOn(symbol string) []domain.Node {
	return g.WhatDoesXDependOnPrecise(symbol, false)
}

// WhatDoesXDependOnPrecise is WhatDoesXDependOn with a precision mode: when
// strict is true, outgoing call edges from a caller whose language is not
// "resolved"-precision are skipped.
func (g *Graph) WhatDoesXDependOnPrecise(symbol string, strict bool) []domain.Node {
	outgoing, _ := g.adjacency(strict)
	reach := transitive(g.resolveSymbol(symbol), outgoing, maxHops)
	return nodesForIDs(g.nodesByID(), reach)
}

// WhatDoesXDependOnNames returns the symbols the given symbol depends on
// as renderable names, preserving callees that do not resolve to indexed
// nodes (import-qualified stdlib calls like "fmt.Println") instead of
// silently dropping them: a method whose every callee is external reported
// "What it calls: 0" via the node-based query while `kern why` showed the
// raw edges (e2e round 2, P0-1). Resolved IDs map to node names; unresolved
// IDs are returned verbatim so impact reports stop under-reporting.
func (g *Graph) WhatDoesXDependOnNames(symbol string, strict bool) []string {
	outgoing, _ := g.adjacency(strict)
	reach := transitive(g.resolveSymbol(symbol), outgoing, maxHops)
	byID := g.nodesByID()
	out := make([]string, 0, len(reach))
	for _, id := range reach {
		if n, ok := byID[id]; ok && n.Symbol != nil {
			if n.Symbol.Qualified != "" {
				out = append(out, n.Symbol.Qualified)
			} else {
				out = append(out, n.Symbol.Name)
			}
			continue
		}
		out = append(out, id)
	}
	return out
}

// DirectDependsOnNames returns the 1-hop callees of symbol as renderable names,
// using the same resolved-or-verbatim mapping as WhatDoesXDependOnNames. It
// exists so impact reports can list direct calls first, then transitive-only
// callees, instead of an undifferentiated alphabetical dump.
func (g *Graph) DirectDependsOnNames(symbol string, strict bool) []string {
	outgoing, _ := g.adjacency(strict)
	start := g.resolveSymbol(symbol)
	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []string
	for _, id := range outgoing[start] {
		name := id
		if n, ok := byID[id]; ok && n.Symbol != nil {
			if n.Symbol.Qualified != "" {
				name = n.Symbol.Qualified
			} else {
				name = n.Symbol.Name
			}
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// WhatAPIsAffected returns the API entry-point nodes affected by a change to
// the given symbol: every node transitively depending on it that is a framework
// entry point, plus the symbol itself when it is one.
func (g *Graph) WhatAPIsAffected(symbol string) []domain.Node {
	return g.WhatAPIsAffectedPrecise(symbol, false)
}

// WhatAPIsAffectedPrecise is WhatAPIsAffected with a precision mode: when
// strict is true, non-"resolved" caller edges are skipped during traversal.
func (g *Graph) WhatAPIsAffectedPrecise(symbol string, strict bool) []domain.Node {
	_, incoming := g.adjacency(strict)
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
	return g.WhatServicesAffectedPrecise(symbol, false)
}

// WhatServicesAffectedPrecise is WhatServicesAffected with a precision mode:
// when strict is true, non-"resolved" caller edges are skipped.
func (g *Graph) WhatServicesAffectedPrecise(symbol string, strict bool) []domain.Node {
	entries := g.WhatAPIsAffectedPrecise(symbol, strict)

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
	return g.WhatEventsAffectedPrecise(symbol, false)
}

// WhatEventsAffectedPrecise is WhatEventsAffected with a precision mode: when
// strict is true, non-"resolved" caller edges are skipped.
func (g *Graph) WhatEventsAffectedPrecise(symbol string, strict bool) []domain.Node {
	// Direct callees (produced/consumed) and direct callers, plus the symbol
	// itself. Depth-1 keeps the result tight and deterministic.
	outgoing, incoming := g.adjacency(strict)
	resolved := g.resolveSymbol(symbol)
	// Defensive copy: incoming is cached on the graph; appending to the
	// shared slice could write into its spare capacity.
	ids := append([]string(nil), incoming[resolved]...)
	ids = append(ids, resolved)
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
	return g.ProductionCriticalityPrecise(symbol, false)
}

// ProductionCriticalityPrecise is ProductionCriticality with a precision mode:
// when strict is true, the blast radius counts only "resolved"-precision
// caller edges.
func (g *Graph) ProductionCriticalityPrecise(symbol string, strict bool) string {
	n := len(g.WhatDependsOnPrecise(symbol, strict))
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

// WhatTestsCover returns the test nodes that cover the given symbol: tests
// that directly exercise it. Coverage is scoped to directly-relevant tests —
// tests in the symbol's own package (same source directory) plus tests that
// directly call the symbol — so the result stays bounded and accurate for hub
// symbols. It deliberately does NOT expand over every test that transitively
// reaches the symbol: for a core library symbol (e.g. an eventbus internals)
// that closure is essentially the whole repo's test suite (thousands of
// unrelated cmd/* tests), which inflated impact reports ("Tests that cover
// it: 3051" for an internal/eventbus symbol), bloated context packets with
// one claim per test, and made the tiktoken measurement take minutes (F3).
func (g *Graph) WhatTestsCover(symbol string) []domain.Node {
	return g.WhatTestsCoverPrecise(symbol, false)
}

// WhatTestsCoverPrecise is WhatTestsCover with a precision mode: when strict
// is true, direct test callers whose caller language is not "resolved"-
// precision are skipped during the direct-caller scan (same-package tests are
// package-scoped and unaffected by precision).
func (g *Graph) WhatTestsCoverPrecise(symbol string, strict bool) []domain.Node {
	resolved, ok := g.resolveNodeID(symbol)
	if !ok {
		return nil
	}
	_, incoming := g.adjacency(strict)
	byID := g.nodesByID()

	seen := map[string]bool{}
	var out []domain.Node
	add := func(id string) {
		if id == resolved {
			return // a test does not cover itself
		}
		n, ok := byID[id]
		if !ok || !isTest(n.Symbol) {
			return
		}
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	// 1. Direct test callers: tests with a direct "calls" edge into the
	// symbol (the adjacency is cached per graph — no per-call rebuild).
	for _, caller := range incoming[resolved] {
		add(caller)
	}
	// 2. Same-package tests: tests defined in the symbol's own package
	// (source directory), from the per-graph cached test map.
	if n, ok := byID[resolved]; ok && n.Symbol != nil && n.Symbol.File != "" {
		for _, id := range g.testsByPackage()[filepath.Dir(n.Symbol.File)] {
			add(id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// testsByPkg lazily builds the per-graph map of package directory → test node
// IDs (cached, so the scoped WhatTestsCover query never re-scans the node set
// per call). Package identity is the source directory of a symbol's file,
// which in Go is the package boundary; the graph is read-only after
// construction, so the cache is invalidated exactly when the index is rebuilt
// and a fresh Graph is constructed (per-root, like the other graph caches).
func (g *Graph) testsByPackage() map[string][]string {
	g.testsOnce.Do(func() {
		m := make(map[string][]string)
		for _, n := range g.Nodes {
			if n.Symbol == nil || n.Symbol.File == "" || !isTest(n.Symbol) {
				continue
			}
			dir := filepath.Dir(n.Symbol.File)
			m[dir] = append(m[dir], n.ID)
		}
		for k := range m {
			sort.Strings(m[k])
		}
		g.testsByPkg = m
	})
	return g.testsByPkg
}

// booleanNested reports whether a dotted qualifier refers to a (possibly
// nested) receiver: the qualifier is more-qualified than the bare receiver
// and its last segment is the receiver itself (e.g. "Outer.Inner" for a
// receiver "Inner"). Mirrors index.ResolveDottedMethod's tier 3.
func booleanNested(qualifier, receiver string) bool {
	if !strings.Contains(qualifier, ".") || receiver == "" {
		return false
	}
	if i := strings.LastIndexByte(qualifier, '.'); i >= 0 {
		return qualifier[i+1:] == receiver
	}
	return false
}
