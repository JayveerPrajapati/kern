package intel

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
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

// ambiguousPrefix marks an endpoint reference whose bare name matches
// several definitions but which could not be resolved to a specific node
// (no package context survives the index's bare-name call buckets). Such a
// reference is a MERGER ARTIFACT: the edge is real (some same-named symbol
// participates) but the index cannot say which one. The traversal treats
// these as opaque: they can be REACHED (the edge is visible) but never
// EXPANDED, so the New/Run/Error hubs cannot amplify a blast radius by
// pooling every same-named definition's callers (deep-dive B4).
const ambiguousPrefix = "?"

// markAmbiguous prefixes ref with ambiguousPrefix when its bare name is
// ambiguous among the graph's definitions (same simple name on nodes in
// different packages). Unresolvable references with NO definitions at all
// (external callees like "fmt.Println") are returned unchanged — they cannot
// merge edges from multiple definitions, so they are safe traversal leaves.
func (g *Graph) markAmbiguous(ref string) string {
	g.initIndex()
	bare := ref
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
	}
	if len(g.nameIndex[bare]) > 1 {
		return ambiguousPrefix + ref
	}
	return ref
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
		from, fromOK := canonical(e.From)
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
		if !fromOK {
			// The caller reference is ambiguous (bare name on several
			// definitions) or unresolvable. An ambiguous caller must not
			// become a traversable node pooling every same-named
			// definition's edges — mark it opaque.
			from = g.markAmbiguous(e.From)
		}
		if !ok {
			// The callee stayed unresolved: when its bare name is
			// ambiguous among definitions, the edge's true target is
			// unknown — mark it opaque so forward traversals reach it but
			// never expand through it. Unambiguous externals stay as-is.
			to = g.markAmbiguous(e.To)
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
		// A Qualified==ref match only disambiguates when it is UNIQUE:
		// every plain "Save" definition carries Qualified "Save", so the
		// first-match rule used to attribute an ambiguous bare reference
		// to an arbitrary same-named node (root cause — one arbitrary
		// pick then pools every same-named definition's callers).
		var qualifiedMatch string
		qualifiedMatches := 0
		var exactMatch, suffixMatch, nestedMatch string
		exactMatches, suffixMatches, nestedMatches := 0, 0, 0
		for _, id := range ids {
			n, ok := g.byID[id]
			if !ok || n.Symbol == nil {
				continue
			}
			if n.Symbol.Qualified == ref {
				qualifiedMatch = id
				qualifiedMatches++
				continue
			}
			if qualifier != "" && n.Symbol.Receiver != "" {
				switch {
				case n.Symbol.Receiver == qualifier:
					exactMatch = id
					exactMatches++
				case strings.HasSuffix(n.Symbol.Receiver, "."+qualifier):
					if suffixMatch == "" {
						suffixMatch = id
					}
					suffixMatches++
				case booleanNested(qualifier, n.Symbol.Receiver):
					if nestedMatch == "" {
						nestedMatch = id
					}
					nestedMatches++
				}
			}
		}
		if qualifiedMatches == 1 {
			return qualifiedMatch, true
		}
		// A receiver-qualified reference ("Store.List", "Outer.Inner.M")
		// resolves only when the receiver+name combination is UNIQUE: the
		// same first-match rule that pooled every same-named definition's
		// callers for ambiguous bare references (see the Qualified==ref
		// comment above) would otherwise attribute a chained/qualified edge
		// to an arbitrary same-receiver definition. The index-side
		// receiver-chain merge already requires this uniqueness
		// (computeCallers, internal/index/engine.go: matches == 1), so an
		// ambiguous receiver match must resolve to nothing — impact and
		// explore then agree on dropping the edge instead of impact
		// guessing the first definition (Phase 5 Part C: the
		// "tasklife.NewTaskService.Store.List" chained edge was
		// over-attributed to internal/gates.Store.List by this first-match).
		if exactMatches == 1 {
			return exactMatch, true
		}
		if suffixMatches == 1 {
			return suffixMatch, true
		}
		if nestedMatches == 1 {
			return nestedMatch, true
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

// DefsWithSimpleName returns the node IDs of every definition carrying the
// given simple name (e.g. "New" defined in three packages). It powers the
// bare-name collision warning (B4, deep-dive 2026-10-03): call edges are
// recorded by bare name, so callers of same-named definitions cannot be told
// apart — impact over-counts for such names and must say so.
func (g *Graph) DefsWithSimpleName(name string) []string {
	g.initIndex()
	ids := g.nameIndex[name]
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
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
	// A package-qualified reference ("memory.Add") names a package-level
	// symbol: methods are never package-qualified in source ("MemoryStore.Add"
	// is receiver-qualified). When the imported package defines the name as
	// BOTH a func and a method, only the receiver-less def can be the target
	// of the qualified reference — the method must not make the resolution
	// ambiguous (internal/memory defines func Add AND method
	// MemoryStore.Add; "memory.Add" from cmd/kern is the func, not the
	// method, and never the base-sharing wrapper internal/mcp/memory.Add).
	var found string
	for _, id := range g.nameIndex[name] {
		localPkg := g.nodePkg[id]
		if localPkg == "" {
			continue
		}
		if !importMatchesQualifier(imports, qual, localPkg) {
			continue
		}
		if n, ok := g.byID[id]; ok && n.Symbol != nil && n.Symbol.Receiver != "" {
			continue // a qualified package reference never names a method
		}
		if found != "" {
			return "", false // ambiguous across imported packages
		}
		found = id
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
// are sorted for determinism. Nodes marked ambiguous (ambiguousPrefix) are
// reached but never expanded: their edges are merger artifacts of the index's
// bare-name call buckets, so walking through them would pool every same-named
// definition's callers into one blast radius.
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
			if strings.HasPrefix(cur, ambiguousPrefix) {
				continue // opaque: reached, never expanded
			}
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

// closureKey identifies one memoized full transitive closure: the precision
// mode, the traversal direction (incoming = reverse/callers, outgoing =
// forward/callees) and the root node ID.
type closureKey struct {
	strict   bool
	incoming bool
	start    string
}

// closure returns the full (maxHops) transitive closure of start — every node
// ID reachable from start, excluding start itself, sorted — in the requested
// direction and precision mode, memoized per (start, direction, strict) for
// the graph's lifetime. The graph is read-only after construction, so the
// memo never goes stale (same reasoning as the adjacency cache). All the
// impact queries (WhatDependsOn, WhatDoesXDependOn, WhatAPIsAffected,
// ProductionCriticality) share this memo, so a multi-query operation like
// kern impact computes each closure once instead of re-walking the reachable
// subgraph per query. The returned slice is shared and must be treated as
// read-only (the one caller that extends it, WhatAPIsAffectedPrecise, copies
// first). Concurrent callers may compute the same closure in parallel; the
// first writer wins and the others adopt that canonical slice.
func (g *Graph) closure(start string, strict, incoming bool) []string {
	key := closureKey{strict: strict, incoming: incoming, start: start}
	g.closureMu.Lock()
	if ids, ok := g.closureMemo[key]; ok {
		g.closureMu.Unlock()
		return ids
	}
	g.closureMu.Unlock()

	var neighbor map[string][]string
	if incoming {
		_, neighbor = g.adjacency(strict)
	} else {
		neighbor, _ = g.adjacency(strict)
	}
	ids := transitive(start, neighbor, maxHops)

	g.closureMu.Lock()
	if g.closureMemo == nil {
		g.closureMemo = make(map[closureKey][]string)
	}
	if existing, ok := g.closureMemo[key]; ok {
		ids = existing // a concurrent caller computed it first; keep one canonical slice
	} else {
		g.closureMemo[key] = ids
	}
	g.closureMu.Unlock()
	return ids
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

// DirectCallersNames returns the direct (1-hop) callers of symbol as
// renderable names, walking the RAW "calls" edges instead of the adjacency
// map so callers are not silently dropped when a qualified/ambiguous edge
// endpoint does not canonicalize to the target's node ID or the caller node
// is absent from the node map (WhoCallsPrecise loses those via nodesForIDs,
// so `kern impact` reported fewer callers than `kern explore`, which reads
// the raw index edges). An edge is kept only when its CALLEE endpoint names
// the target's node: a foreign/unresolvable endpoint (qualifier matching no
// caller import) does not call the target and never attributes a caller. The
// target-anchored fallbacks keep edges whose callee IS the target even when
// the caller endpoint is an ambiguous bare name (New →
// governance.AuthorizeContext), which ResolveEdgeEndpoint alone drops
// because its caller pre-condition fails before the import-qualified callee
// resolution can run. Caller endpoints resolve package-aware (same-package,
// then import-based against the callee's package); an unresolved/ambiguous
// caller is kept verbatim rather than dropped — it is still a real caller.
// Strict precision mirrors the adjacency: edges whose caller language is not
// "resolved"-precision are skipped (unknown rather than guessed). Results
// are first-seen in graph edge order, deduplicated.
func (g *Graph) DirectCallersNames(symbol string, strict bool) []string {
	target := g.resolveSymbol(symbol)

	// Language per node ID, needed only for the strict precision filter
	// (mirrors buildAdjacencyOpt's langByID).
	langByID := map[string]string{}
	if strict {
		for _, n := range g.Nodes {
			if n.Symbol != nil && n.Symbol.Language != "" {
				langByID[n.ID] = n.Symbol.Language
			}
		}
	}

	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []string
	for _, e := range g.Edges {
		if e.Kind != "calls" {
			continue
		}
		// Callee endpoint: keep the edge only when it names the target.
		// An unresolvable callee (e.g. a foreign qualifier matching no
		// caller import) does not call the target and never attributes a
		// caller.
		if !g.calleeIsTarget(e, target) {
			continue
		}
		// Caller endpoint: resolve to a node ID when possible — package-aware
		// (same-package, then import-based against the callee's package) when
		// the bare name is shared by several packages — and keep the RAW
		// endpoint verbatim when the caller cannot be attributed: an
		// unresolved/ambiguous caller is still a real caller and must be
		// reported (explore renders such callers without a location, never
		// fabricated).
		from := e.From
		if fromID, ok := g.resolveCallerNodeID(e.From, target, false); ok {
			if strict {
				if p := g.precisionByLang[langByID[fromID]]; p != "resolved" {
					continue
				}
			}
			// A caller that resolves to the target itself is the symbol's
			// own recursion edge, not a caller: computeCallers deliberately
			// skips self-edges on the index side (a symbol never reports
			// itself in explore's caller list), so impact must not count
			// them either — the parity contract between the two surfaces.
			if fromID == target {
				continue
			}
			from = fromID
		} else if strict {
			// An unresolvable caller is unknown, not a guess — the same
			// contract as the strict adjacency, whose precision lookup on
			// the raw endpoint always misses "resolved".
			continue
		}
		if seen[from] {
			continue
		}
		seen[from] = true
		name := from
		if n, ok := byID[from]; ok && n.Symbol != nil {
			if n.Symbol.Qualified != "" {
				name = n.Symbol.Qualified
			} else {
				name = n.Symbol.Name
			}
		}
		out = append(out, name)
	}
	return out
}

// endpointNamesNode reports whether a raw "calls" edge endpoint names the
// target node via its receiver-qualified form. The raw edges record method
// calls receiver-qualified ("Server.dispatch") while node IDs are
// package-scoped ("internal/bpcli/mcp.Server.dispatch"), and the bare-name
// index cannot disambiguate a receiver-qualified name shared by methods in
// different packages (two packages may each define Server.dispatch). The
// node's own receiver type + name is the most specific evidence the
// endpoint carries; it is checked only after every resolution attempt has
// failed, so it never overrides an import-qualified or unique resolution.
func (g *Graph) endpointNamesNode(ref, target string) bool {
	g.initIndex()
	tn, ok := g.byID[target]
	if !ok || tn.Symbol == nil || tn.Symbol.Receiver == "" {
		return false
	}
	qual, bare := endpointParts(ref)
	return qual == tn.Symbol.Receiver && bare == tn.Symbol.Name
}

// calleeIsTarget reports whether a "calls" edge's callee endpoint names the
// target node. The direct/import-qualified resolution (ResolveEdgeEndpoint)
// is tried first; when its caller pre-condition fails — the caller endpoint
// is an ambiguous bare name, or the callee is a bare name with no qualifier
// to link — two package-aware fallbacks run, anchored on the caller's own
// package and on the target's own package respectively. A foreign or
// unresolvable callee (e.g. "fmt.Println", "buf.Save") never matches.
func (g *Graph) calleeIsTarget(e domain.Edge, target string) bool {
	// A bare callee that is a Go predeclared identifier ("append", "len")
	// binds to the builtin unless the CALLER's package shadows it with a
	// package-level definition (Go scoping; methods are never bare-callable,
	// so a method named "append" never shadows the builtin). The
	// project-wide unique-name resolution must not capture every builtin
	// call for the one project symbol that happens to define the name
	// (internal/gates Store.append): only a same-package receiver-less
	// definition can be the callee.
	if !strings.Contains(e.To, ".") && goPredeclared[e.To] {
		if callerID, ok := g.resolveNodeID(e.From); ok {
			if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok {
				if tn, ok2 := g.byID[id]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" {
					return false
				}
				return id == target
			}
		}
		return false
	}
	// A bare callee from a receiver-qualified caller is resolved
	// package-aware FIRST: inside a method, a bare reference resolves to the
	// caller's own package — a package-level func in Go, or the receiver
	// type's method in TS (the extractor records `this.get(...)` as a bare
	// "get"). Without this the unique receiver-less definition project-wide
	// (the internal/web helper "get") hijacks every Client method's call,
	// and impact reports the TS Client.get callers against the web helper
	// instead of the method. Every receiver-matched candidate is tried (the
	// caller key "Client.audit" is pooled by the Python and TS SDKs); if a
	// candidate's package resolves the callee, that resolution is
	// authoritative for the edge.
	if !strings.Contains(e.To, ".") {
		if qual, bare := endpointParts(e.From); qual != "" && bare != "" {
			resolved := false
			for _, cid := range g.nameIndex[bare] {
				n, ok := g.byID[cid]
				if !ok || n.Symbol == nil || n.Symbol.Receiver != qual {
					continue
				}
				if id, ok := g.resolveEndpointPackageAware(e.To, cid, false, false); ok {
					resolved = true
					if id == target {
						return true
					}
				}
			}
			if resolved {
				return false
			}
		}
	}
	if toID, ok := g.ResolveEdgeEndpoint(e.To, e.From); ok {
		if toID == target {
			// A BARE reference never names a method under GO SCOPING (a bare
			// call binds to a package-level func or the builtin). The
			// unique-simple-name resolution must not capture every bare
			// call for a method that happens to be the only symbol of its
			// name ("check" is unique because only Schema.check exists, but
			// CheckBoundariesPrecise's bare check() is not a call to it).
			// The rule is Go-only: in TS/Python/Java a bare reference CAN
			// name a method (`this.greet()` is recorded bare "greet" by the
			// extractor), so a foreign resolved method is a real callee
			// (A5, Phase 5 — verified per-language by
			// TestParityReportPerLanguage's guard model).
			if !strings.Contains(e.To, ".") {
				if tn, ok2 := g.byID[toID]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" && tn.Symbol.Language == "go" {
					return false
				}
			}
			return true
		}
		// A node-ID direct-hit on the raw endpoint (a root-package symbol
		// whose bare node ID collides with the reference — the CHANGELOG.md
		// heading "Changelog" is a node whose ID is the bare name) resolved
		// the callee WITHOUT disambiguation. That resolution is provisional:
		// fall through to the package-aware fallbacks below, which anchor on
		// the caller's package and only match when the endpoint genuinely
		// names the target (the Go tests call internal/commitmsg.Changelog,
		// never the markdown heading). Any other (import-qualified /
		// unique-name) resolution is authoritative: a callee that resolved
		// to a different symbol does not call the target.
		if _, direct := g.byID[e.To]; !direct {
			return false
		}
	}
	g.initIndex()
	// A bare callee referenced from the caller's own package resolves to
	// the same-package candidate (a package holds at most one definition of
	// any name), so the target's own-package edges survive even though the
	// callee is a bare ambiguous name ("AuthorizeContext" from
	// authorize_test.go is the core, not the MCP wrapper).
	if callerID, ok := g.resolveNodeID(e.From); ok {
		if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok && id == target {
			// A BARE reference never names a method under GO SCOPING (a bare
			// call binds to a package-level func or the builtin). The
			// caller-anchored resolution of a bare callee must not land on
			// a same-named method (a bare "check" in a package importing
			// internal/schema is not Schema.check), or every import-linked
			// bare call would pool onto the method. Go-only, mirroring the
			// guard above: foreign receiver-less method edges (TS
			// `this.greet()`, Ruby file-owned `install`) are real callees
			// (A5, Phase 5).
			if !strings.Contains(e.To, ".") {
				if tn, ok2 := g.byID[id]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" && tn.Symbol.Language == "go" {
					return false
				}
			}
			return true
		}
		// The package-aware resolution attributed the callee to a DIFFERENT
		// same-named node (the caller's own package wins). That is correct
		// for a bare ambiguous callee, but a receiver-qualified endpoint
		// ("Server.dispatch") still names this target when its qualifier is
		// the target's receiver — fall through to that check instead of
		// dropping the edge (the index attributes such edges to every
		// same-named method, and explore reports them for each).
	}
	// An import-qualified callee ("governance.AuthorizeContext") whose
	// caller is itself an ambiguous bare name (New, Generate): the qualifier
	// names the target's own package, so the callee IS the target — the edge
	// must not be dropped on the caller pre-condition. The qualifier is a
	// PACKAGE name, so it can only name a package-level (receiver-less)
	// symbol: "tokenize.Count" is the func Count's qualified form and must
	// not be base-matched onto the method Estimator.Count just because both
	// live in internal/tokenize.
	if i := strings.LastIndexByte(e.To, '.'); i > 0 && i < len(e.To)-1 {
		targetPkg := g.nodePkg[target]
		if targetPkg != "" && e.To[:i] == lastPathSeg(targetPkg) {
			if tn, ok := g.byID[target]; ok && tn.Symbol != nil && tn.Symbol.Receiver != "" {
				return false
			}
			for _, id := range g.nameIndex[e.To[i+1:]] {
				if id == target {
					return true
				}
			}
		}
	}
	// A receiver-qualified callee ("Server.dispatch") whose caller is itself
	// an ambiguous receiver-qualified name ("Server.Serve"): neither side
	// carries package context, but the endpoint names the target's own
	// receiver + name, so the edge calls the target. Without this, impact
	// reported 0 callers for a method whose edges explore resolves (both
	// endpoints are ambiguous whenever two packages define the same method
	// name on the same receiver type).
	if g.endpointNamesNode(e.To, target) {
		return true
	}
	return false
}

// resolveCallerNodeID resolves a "calls" edge's CALLER endpoint to a node ID,
// package-aware. A unique or qualified endpoint resolves directly; an
// ambiguous bare name (the simple name is shared by several packages) is
// attributed with the callee node as the anchor (see
// resolveEndpointPackageAware). Returns false when the caller cannot be
// attributed — the caller is then reported under its raw endpoint verbatim,
// never fabricated. skipSamePackage suppresses the same-package preference:
// a caller of a package-qualified callee never lives in the callee's own
// package (self-package calls are recorded bare), so a same-named symbol in
// the callee's package must not win the attribution for another package's
// edges (the governance core vs its MCP wrapper).
func (g *Graph) resolveCallerNodeID(callerRef string, calleeID string, skipSamePackage bool) (string, bool) {
	if id, ok := g.resolveNodeID(callerRef); ok {
		return id, true
	}
	return g.resolveEndpointPackageAware(callerRef, calleeID, true, skipSamePackage)
}

// resolveEndpointPackageAware attributes an endpoint whose bare name is
// ambiguous (shared by several packages) using the anchor node on the other
// side of the edge — for a caller endpoint the callee, for a callee endpoint
// the caller. The anchor itself is excluded (an edge never attributes
// itself), then a candidate in the anchor's own package is preferred (a
// same-package reference needs no import), then an import-based match: for a
// caller, its package must import the callee's package; for a callee, the
// caller's package must import its package. A qualified endpoint's qualifier
// must name the candidate's own package (its final path segment). Returns
// false when the endpoint cannot be attributed.
func (g *Graph) resolveEndpointPackageAware(ref, anchorID string, callerSide, skipSamePackage bool) (string, bool) {
	g.initIndex()
	qual, bare := endpointParts(ref)
	ids := g.nameIndex[bare]
	if len(ids) == 0 {
		return "", false
	}
	anchor, ok := g.byID[anchorID]
	if !ok {
		return "", false
	}
	var anchorDir string
	if anchor.Symbol != nil {
		anchorDir = filepath.Dir(anchor.Symbol.File)
	}
	cand := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != anchorID {
			cand = append(cand, id)
		}
	}
	if len(cand) == 0 {
		return "", false
	}
	// A qualified endpoint's qualifier names the candidate's own package — or,
	// for a receiver-qualified method endpoint ("Server.dispatch"), the
	// candidate's receiver type: a candidate whose package does not end in the
	// qualifier and whose receiver is not the qualifier cannot be the
	// referenced symbol (a type/method qualifier matches no package and
	// attributes nothing).
	if qual != "" {
		filtered := cand[:0]
		for _, id := range cand {
			n, ok := g.byID[id]
			if lastPathSeg(g.nodePkg[id]) == qual {
				filtered = append(filtered, id)
			} else if ok && n.Symbol != nil && n.Symbol.Receiver == qual {
				filtered = append(filtered, id)
			}
		}
		cand = filtered
		if len(cand) == 0 {
			return "", false
		}
	}
	// Same-package preference: the same-package candidate needs no import,
	// so it would lose the import-based tie-break to a cross-package
	// same-named symbol that merely imports (or is imported by) the anchor's
	// package.
	if !skipSamePackage {
		var local []string
		for _, id := range cand {
			if n, ok := g.byID[id]; ok && n.Symbol != nil && filepath.Dir(n.Symbol.File) == anchorDir {
				local = append(local, id)
			}
		}
		switch len(local) {
		case 1:
			return local[0], true
		case 0:
		default:
			cand = local
		}
	}
	// Import-based match: for a caller, its package must import the
	// anchor's (callee's) package; for a callee, the anchor's (caller's)
	// package must import its package. A BARE predeclared identifier
	// ("append", "len") is never import-resolved: Go scoping binds it to
	// the builtin when the caller's package does not shadow it, so an
	// import of a package defining a same-named symbol must not capture the
	// builtin call (Store.append in internal/gates).
	if qual == "" && goPredeclared[bare] {
		return "", false
	}
	anchorPkg := g.nodePkg[anchorID]
	if anchorPkg == "" {
		return "", false
	}
	var imp []string
	for _, id := range cand {
		pkg := g.nodePkg[id]
		if pkg == "" {
			continue
		}
		if callerSide {
			if importMatchesQualifier(g.pkgImports[pkg], lastPathSeg(anchorPkg), anchorPkg) {
				imp = append(imp, id)
			}
		} else {
			if importMatchesQualifier(g.pkgImports[anchorPkg], lastPathSeg(pkg), pkg) {
				imp = append(imp, id)
			}
		}
	}
	if len(imp) == 1 {
		return imp[0], true
	}
	return "", false
}

// endpointParts splits an endpoint reference into its qualifier (the part
// before the last dot, "" for bare names) and its bare simple name.
func endpointParts(ref string) (qual, bare string) {
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// lastPathSeg returns the final path segment of a package path (the
// qualifier Go source uses to reference it).
func lastPathSeg(pkg string) string {
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// goPredeclared is the set of Go predeclared identifiers (builtin functions,
// types, constants). A bare callee with one of these names and no
// same-package package-level declaration is the builtin (Go scoping) — never
// an unrelated package's same-named symbol, and never a method (methods are
// not bare-callable). Mirrors the index's isPredeclared.
var goPredeclared = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true,
	"true": true, "false": true, "iota": true, "nil": true,
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
	reach := g.closure(g.resolveSymbol(symbol), strict, true)
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
	reach := g.closure(g.resolveSymbol(symbol), strict, false)
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
	reach := g.closure(g.resolveSymbol(symbol), strict, false)
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
// callees, instead of an undifferentiated alphabetical dump. Edges are walked
// raw: an edge belongs to the target only when its CALLER endpoint resolves
// to the target's node — attributed package-aware when the target's bare name
// is shared by several packages (its own edges are recorded under that bare
// name, which the adjacency's opaque "?"-bucket would otherwise lose, so
// `kern impact` reported "What it calls: 0" for e.g. the governance core).
func (g *Graph) DirectDependsOnNames(symbol string, strict bool) []string {
	target := g.resolveSymbol(symbol)

	// Language per node ID, needed only for the strict precision filter
	// (mirrors buildAdjacencyOpt's langByID).
	langByID := map[string]string{}
	if strict {
		for _, n := range g.Nodes {
			if n.Symbol != nil && n.Symbol.Language != "" {
				langByID[n.ID] = n.Symbol.Language
			}
		}
	}

	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []string
	for _, e := range g.Edges {
		if e.Kind != "calls" {
			continue
		}
		// Caller endpoint: the edge belongs to the target only when the
		// caller resolves to it. A target whose bare name is shared by
		// several packages is attributed package-aware via the callee's
		// package.
		fromID, ok := g.resolveNodeID(e.From)
		if !ok {
			// A receiver-qualified caller endpoint naming the target
			// directly ("Server.dispatch" for a Server-method target) is
			// the most specific evidence the endpoint carries — try it
			// before the callee-anchored attribution so a same-package
			// method call cannot be mis-attributed to a same-named method
			// in an importing package.
			if g.endpointNamesNode(e.From, target) {
				fromID = target
			} else {
				calleeID, cok := g.ResolveEdgeEndpoint(e.To, e.From)
				if !cok {
					// Unresolvable callee AND ambiguous caller: the edge cannot
					// be attributed to any package — drop it rather than guess.
					continue
				}
				fromID, ok = g.resolveCallerNodeID(e.From, calleeID, strings.Contains(e.To, "."))
				if !ok {
					continue
				}
			}
		}
		if fromID != target {
			continue
		}
		if strict {
			if p := g.precisionByLang[langByID[fromID]]; p != "resolved" {
				continue
			}
		}
		// Callee endpoint: resolved names where possible, the raw endpoint
		// verbatim otherwise — a foreign callee like "fmt.Println" survives
		// instead of emptying the section (WhatDoesXDependOnNames parity).
		name := e.To
		if toID, ok := g.ResolveEdgeEndpoint(e.To, e.From); ok {
			if n, ok2 := byID[toID]; ok2 && n.Symbol != nil {
				if n.Symbol.Qualified != "" {
					name = n.Symbol.Qualified
				} else {
					name = n.Symbol.Name
				}
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
	resolved := g.resolveSymbol(symbol)
	reach := g.closure(resolved, strict, true)

	byID := g.nodesByID()
	seen := map[string]bool{}
	var out []domain.Node
	// The symbol itself too: a change to an entry-point handler affects it.
	// reach is a shared memoized slice — copy before append so the memo's
	// backing array is never extended in place.
	ids := append(append([]string(nil), reach...), resolved)
	for _, id := range ids {
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

// isTest reports whether a symbol is a test node by its language's own
// convention: Go keeps the historical rule (Test-prefixed name or a
// _test.go file) byte-for-byte; a Python symbol counts when its file is
// test_*.py / *_test.py or its name is test_-prefixed; a TS/JS symbol
// counts when its file is *.test.ts(x)/js(x) or *.spec.ts(x)/js(x).
// Symbols of other (or unknown) languages keep the Go rule. Doc symbols
// never qualify: a markdown heading named "Test with curl" or "Testing
// Commands" matched the prefix alone and polluted plan validation steps
// with prose (deep-dive A2/F3, 2026-10-03) — the same class of doc
// pollution the AffectedFiles doc filter already guards against.
// Language awareness is F7 (2026-10-04): `kern impact` on Python repos
// listed pytest functions as direct callers yet reported "Tests that
// cover it: 0" because recognition was Go-shaped.
func isTest(s *domain.Symbol) bool {
	if s == nil {
		return false
	}
	if s.Kind == "heading" || index.IsDocFile(s.File) {
		return false
	}
	name, file := s.Name, s.File
	switch {
	case strings.HasSuffix(file, ".py"):
		return strings.HasPrefix(strings.ToLower(filepath.Base(file)), "test_") ||
			strings.HasSuffix(strings.ToLower(file), "_test.py") ||
			strings.HasPrefix(name, "test_")
	case strings.HasSuffix(file, ".ts"), strings.HasSuffix(file, ".tsx"),
		strings.HasSuffix(file, ".js"), strings.HasSuffix(file, ".jsx"),
		strings.HasSuffix(file, ".mjs"), strings.HasSuffix(file, ".cjs"):
		lower := strings.ToLower(file)
		for _, suffix := range []string{
			".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".test.mjs", ".test.cjs",
			".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx", ".spec.mjs", ".spec.cjs",
		} {
			if strings.HasSuffix(lower, suffix) {
				return true
			}
		}
		return false
	default:
		return strings.HasPrefix(name, "Test") || strings.Contains(file, "_test.go")
	}
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
	// The ranked variant computes the same scoped covering set (same-package
	// tests + direct test callers); this view returns the union sorted by ID
	// so existing callers (emptiness checks, hub coverage) see the bounded
	// set exactly as before — only the presentation is tiered.
	ranked, rest := g.WhatTestsCoverRanked(symbol, strict)
	out := append(ranked, rest...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TestCoverageKind ranks a covering test's relevance to the symbol it covers.
// The impact render lists tiers 1-3 and relegates tier 4 to a count-only
// line, so "Tests that cover it" never implies the whole package covers the
// symbol (the bug-hunter finding: runTaint's impact report listed 431
// same-package cmd tests as covering tests while the actual covering test,
// TestParseTaintRange in cmd_security_test.go, was buried in the noise).
type TestCoverageKind int

const (
	// TestCoverageFilePaired is a test in the symbol's own _test.go twin
	// (cmd_security.go → cmd_security_test.go) — the strongest signal.
	TestCoverageFilePaired TestCoverageKind = iota
	// TestCoverageNameMatched is a test whose name shares a case-insensitive
	// token with the symbol name (runTaint ↔ TestParseTaintRange via "taint").
	TestCoverageNameMatched
	// TestCoverageDirectCaller is a test with a direct "calls" edge into the
	// symbol.
	TestCoverageDirectCaller
	// TestCoverageSamePackage is the same-package remainder — tests that
	// share the symbol's package but neither pair with its file, match its
	// name, nor call it directly. Bounded (the F3 boundary), but weak
	// evidence, so the render shows it as a count, not a list.
	TestCoverageSamePackage
)

// WhatTestsCoverRanked returns the tests covering symbol split by relevance
// tier: ranked holds the file-paired, name-matched and direct-caller tests
// (in that order); rest holds the same-package remainder. The covering set
// itself is unchanged — same-directory tests plus direct test callers (the
// WhatTestsCoverPrecise boundary, kept) — only its presentation is ranked.
func (g *Graph) WhatTestsCoverRanked(symbol string, strict bool) (ranked, rest []domain.Node) {
	resolved, ok := g.resolveNodeID(symbol)
	if !ok {
		return nil, nil
	}
	_, incoming := g.adjacency(strict)
	byID := g.nodesByID()
	n, _ := byID[resolved]
	var symFile string
	if n.Symbol != nil {
		symFile = n.Symbol.File
	}
	paired := pairedTestFile(symFile)
	simple := symbolSimpleName(symbol)

	// The scoped covering set (same-package + direct callers), merged so a
	// test in both buckets is tiered once, at its best tier.
	covered := map[string]domain.Node{}
	if n.Symbol != nil && n.Symbol.File != "" {
		for _, id := range g.testsByPackage()[filepath.Dir(n.Symbol.File)] {
			if nn, ok := byID[id]; ok {
				covered[id] = nn
			}
		}
	}
	direct := map[string]bool{}
	for _, caller := range incoming[resolved] {
		direct[caller] = true
		if nn, ok := byID[caller]; ok {
			covered[caller] = nn
		}
	}

	var filePaired, nameMatched, directCallers, samePkg []domain.Node
	for id, nn := range covered {
		if id == resolved || !isTest(nn.Symbol) {
			continue // a test does not cover itself; only tests cover
		}
		var f string
		if nn.Symbol != nil {
			f = nn.Symbol.File
		}
		switch {
		case paired != "" && f == paired:
			filePaired = append(filePaired, nn)
		case testNameMatchesSymbol(nn, simple):
			nameMatched = append(nameMatched, nn)
		case direct[id]:
			directCallers = append(directCallers, nn)
		default:
			samePkg = append(samePkg, nn)
		}
	}
	sortNodes := func(ns []domain.Node) {
		sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
	}
	sortNodes(filePaired)
	sortNodes(nameMatched)
	sortNodes(directCallers)
	sortNodes(samePkg)
	ranked = append(ranked, filePaired...)
	ranked = append(ranked, nameMatched...)
	ranked = append(ranked, directCallers...)
	return ranked, samePkg
}

// pairedTestFile returns the _test.go twin of a Go source file
// ("cmd/kern/cmd_security.go" → "cmd/kern/cmd_security_test.go"); "" for
// non-Go files and test files themselves.
func pairedTestFile(file string) string {
	if file == "" || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return ""
	}
	return file[:len(file)-len(".go")] + "_test.go"
}

// symbolSimpleName returns the last dotted segment of a (possibly qualified)
// symbol reference — the name tests token-match against ("pkg.runTaint" →
// "runTaint").
func symbolSimpleName(symbol string) string {
	if i := strings.LastIndexByte(symbol, '.'); i >= 0 {
		return symbol[i+1:]
	}
	return symbol
}

// testNameMatchesSymbol reports whether the test's name shares a
// case-insensitive token with the symbol's simple name ("runTaint" ↔
// "TestParseTaintRange" share "taint"). Tokens split on camelCase boundaries
// and non-alphanumeric runs.
func testNameMatchesSymbol(nn domain.Node, simple string) bool {
	if simple == "" || nn.Symbol == nil || nn.Symbol.Name == "" {
		return false
	}
	toks := symbolNameTokens(nn.Symbol.Name)
	for t := range symbolNameTokens(simple) {
		if toks[t] {
			return true
		}
	}
	return false
}

// symbolNameTokens splits a symbol/test name into lowercased camelCase and
// separator tokens ("TestParseTaintRange" → test, parse, taint, range).
func symbolNameTokens(name string) map[string]bool {
	out := map[string]bool{}
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out[strings.ToLower(string(cur))] = true
		cur = cur[:0]
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if len(cur) > 0 && unicode.IsUpper(r) && !unicode.IsUpper(cur[len(cur)-1]) {
				flush()
			}
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
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
