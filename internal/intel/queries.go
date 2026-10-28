package intel

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
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
		if e.Resolution != "" {
			if strict && (e.Resolution != "resolved" || g.precisionByLang[langByID[e.From]] != "resolved") {
				continue
			}
			outgoing[e.From] = append(outgoing[e.From], e.To)
			incoming[e.To] = append(incoming[e.To], e.From)
			continue
		}
		from, fromOK := canonical(e.From)
		if strict {
			if p := g.precisionByLang[langByID[from]]; p != "resolved" {
				continue
			}
		}
		to, ok := canonical(e.To)
		// A foreign-qualified callee ("http.DefaultClient.Do") never names a
		// local symbol: bypass the unique-name resolution so the edge links to
		// the raw external key instead of the same-named project symbol
		// (parity with the index's CallersFor, which never attributes foreign
		// keys — TestForeignCalleeNeverAliasesLocalSymbol).
		if g.foreignCallee(e.To) {
			ok = false
			to = e.To
		}
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
		g.labelIndex = make(map[string][]string)
		for _, n := range g.Nodes {
			g.byID[n.ID] = n
			if n.Symbol != nil {
				g.nameIndex[n.Symbol.Name] = append(g.nameIndex[n.Symbol.Name], n.ID)
				g.labelIndex[n.Label] = append(g.labelIndex[n.Label], n.ID)
			}
		}
		g.nodePkg = make(map[string]string)
		g.pkgImports = make(map[string][]string)
		for _, n := range g.Nodes {
			if n.Symbol == nil || n.Symbol.Qualified == "" {
				continue
			}
			if n.Package != "" {
				g.nodePkg[n.ID] = n.Package
				continue
			}
			if strings.HasPrefix(n.ID, "decl:") {
				g.nodePkg[n.ID] = filepath.Dir(n.Symbol.File)
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
		// Foreign import bases: bases of imported package paths ("http",
		// "fmt") that are not project package directory bases. The index's
		// foreignImportBases uses ix.Pkgs + pkg.Imports; here the module
		// nodes carry the package paths and pkgImports the import paths, so
		// the same set is derivable from the graph alone.
		dirBases := map[string]bool{}
		for _, n := range g.Nodes {
			if n.Kind != "module" {
				continue
			}
			if base := filepath.Base(n.ID); base != "" {
				dirBases[base] = true
			}
		}
		g.foreignBases = map[string]bool{}
		for _, imps := range g.pkgImports {
			for _, imp := range imps {
				if base := filepath.Base(imp); base != "" && !dirBases[base] {
					g.foreignBases[base] = true
				}
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
	if ids := g.labelIndex[ref]; len(ids) > 0 {
		if len(ids) == 1 {
			return ids[0], true
		}
		return ref, false
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
			if strings.HasPrefix(cur, ambiguousPrefix) || strings.HasPrefix(cur, "ref:") {
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
	seenNames := map[string]bool{}
	var out []string
	for _, ei := range g.calleeIndex()[target] {
		e := g.Edges[ei]
		// The callee endpoint names the target by construction (calleeIndex
		// is built from calleeCandidates, the per-edge target-independent
		// form of calleeIsTarget), so only caller attribution remains.
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
		} else if n, ok := byID[from]; ok && n.Label != "" {
			name = n.Label
		}
		if !seenNames[name] {
			seenNames[name] = true
			out = append(out, name)
		}
	}
	return out
}

// endpointNamesNode reports whether a raw "calls" edge endpoint names the
// target node via its receiver-qualified form. The raw edges record method
// calls receiver-qualified ("Server.dispatch") while node IDs are
// package-scoped ("internal/mcp/server.dispatch"), and the bare-name
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

// foreignCallee reports whether a recorded callee reference's first qualifier
// is a foreign package import base ("http.DefaultClient.Do" — the stdlib http
// client method). Such a callee can never name a local symbol, even when its
// bare name is unique; the index side enforces the same contract (CallersFor's
// widening and TestForeignCalleeNeverAliasesLocalSymbol).
func (g *Graph) foreignCallee(ref string) bool {
	if i := strings.IndexByte(ref, '.'); i > 0 {
		return g.foreignBases[ref[:i]]
	}
	return false
}

// calleeIndex indexes every "calls" edge by the node IDs its callee endpoint
// can name (calleeCandidates), built once per graph. DirectCallersNames then
// scans only the edges incident to the queried target instead of re-resolving
// every edge for each of a hub type's expanded roots.
func (g *Graph) calleeIndex() map[string][]int {
	g.calleeOnce.Do(func() {
		m := make(map[string][]int, len(g.Edges))
		for i, e := range g.Edges {
			if e.Kind != "calls" {
				continue
			}
			for _, id := range g.calleeCandidates(e) {
				m[id] = append(m[id], i)
			}
		}
		g.calleeEdges = m
	})
	return g.calleeEdges
}

// calleeCandidates returns every node ID an edge's callee endpoint can name
// under calleeIsTarget's resolution rules — the per-edge, target-independent
// form of that predicate. The two MUST agree; TestCalleeCandidatesParity
// sweeps every edge against every node, and the branch order below mirrors
// calleeIsTarget's short-circuits (an authoritative branch ends the search).
func (g *Graph) calleeCandidates(e domain.Edge) []string {
	g.initIndex()
	if e.Resolution != "" {
		return []string{e.To}
	}
	if g.foreignCallee(e.To) {
		return nil
	}
	bare := !strings.Contains(e.To, ".")
	if bare && goPredeclared[e.To] {
		if callerID, ok := g.resolveNodeID(e.From); ok {
			if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok && !g.goMethod(id) {
				return []string{id}
			}
		}
		return nil
	}
	// A bare callee from a receiver-qualified caller resolves package-aware
	// first; any resolution is authoritative for the edge.
	if bare {
		if qual, name := endpointParts(e.From); qual != "" && name != "" {
			var ids []string
			resolved := false
			for _, cid := range g.nameIndex[name] {
				n, ok := g.byID[cid]
				if !ok || n.Symbol == nil || n.Symbol.Receiver != qual {
					continue
				}
				if id, ok := g.resolveEndpointPackageAware(e.To, cid, false, false); ok {
					resolved = true
					if !g.goMethod(id) {
						ids = append(ids, id)
					}
				}
			}
			if resolved {
				return dedupIDs(ids)
			}
		}
	}
	var out []string
	if toID, ok := g.ResolveEdgeEndpoint(e.To, e.From); ok {
		if !(bare && g.goMethod(toID)) {
			out = append(out, toID)
		}
		if _, direct := g.byID[e.To]; !direct {
			// An authoritative non-direct resolution: only toID can be the
			// target, and it was excluded above when Go scoping forbids it.
			return dedupIDs(out)
		}
	}
	if callerID, ok := g.resolveNodeID(e.From); ok {
		if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok && !(bare && g.goMethod(id)) {
			out = append(out, id)
		}
	}
	// A qualified callee names either a receiver-less symbol in the package
	// whose final path segment is the qualifier, or the method on that
	// receiver. A package-qualified match on a method rejects the edge
	// outright (calleeIsTarget returns false before its endpoint fallback).
	if qual, name := endpointParts(e.To); qual != "" {
		for _, id := range g.nameIndex[name] {
			n, ok := g.byID[id]
			if !ok || n.Symbol == nil {
				continue
			}
			if pkg := g.nodePkg[id]; pkg != "" && lastPathSeg(pkg) == qual {
				if n.Symbol.Receiver == "" {
					out = append(out, id)
				}
				continue
			}
			if n.Symbol.Receiver == qual {
				out = append(out, id)
			}
		}
	}
	return dedupIDs(out)
}

// goMethod reports whether id is a Go method node: a bare reference never
// names one under Go scoping (a bare call binds to a package-level func or
// the builtin).
func (g *Graph) goMethod(id string) bool {
	n, ok := g.byID[id]
	return ok && n.Symbol != nil && n.Symbol.Receiver != "" && n.Symbol.Language == "go"
}

// nodesNamedByEndpoint returns the node IDs a receiver-qualified raw endpoint
// can name directly (its qualifier is the node's receiver and its bare name
// the node's name) — the target-independent set behind endpointNamesNode.
func (g *Graph) nodesNamedByEndpoint(ref string) []string {
	qual, name := endpointParts(ref)
	if qual == "" || name == "" {
		return nil
	}
	var out []string
	for _, id := range g.nameIndex[name] {
		if n, ok := g.byID[id]; ok && n.Symbol != nil && n.Symbol.Receiver == qual {
			out = append(out, id)
		}
	}
	return out
}

func dedupIDs(ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[string]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
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
		if node, found := byID[id]; found && node.Label != "" {
			out = append(out, node.Label)
		} else {
			out = append(out, id)
		}
	}
	sort.Strings(out)
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
// edgeCallerAttr is the per-edge, target-independent caller attribution
// used by DirectDependsOnNames (built once per graph by callerIndex):
// the resolved caller node ID when the raw endpoint resolves, otherwise
// the callee-anchored attribution, with both raw endpoints retained for
// the target-dependent fallback and the callee naming pass.
type edgeCallerAttr struct {
	idx        int
	from, to   string
	resolved   string
	resolvedOK bool
	anchored   string
	anchoredOK bool
}

// callerIndex builds (once) the per-edge caller attribution that lets
// DirectDependsOnNames serve repeated queries without re-resolving every
// edge per call, plus callerEdges: the same attribution inverted into
// target → edge indices so a query scans only incident edges. Resolution
// order and outcomes match the un-memoized form exactly: resolveNodeID first;
// on failure the callee-anchored attribution (the target-dependent
// endpointNamesNode fallback is indexed under every node the raw caller
// endpoint can name, and the query re-applies the precedence).
func (g *Graph) callerIndex() []edgeCallerAttr {
	g.callerOnce.Do(func() {
		attr := make([]edgeCallerAttr, 0, len(g.Edges))
		for i, e := range g.Edges {
			if e.Kind != "calls" {
				continue
			}
			a := edgeCallerAttr{idx: i, from: e.From, to: e.To}
			if id, ok := g.resolveNodeID(e.From); ok {
				a.resolved, a.resolvedOK = id, true
			} else if calleeID, cok := g.ResolveEdgeEndpoint(e.To, e.From); cok {
				if id, aok := g.resolveCallerNodeID(e.From, calleeID, strings.Contains(e.To, ".")); aok {
					a.anchored, a.anchoredOK = id, true
				}
			}
			attr = append(attr, a)
		}
		edges := make(map[string][]int, len(attr))
		for ai, a := range attr {
			if a.resolvedOK {
				// A resolved caller endpoint is authoritative: the
				// endpoint-named fallback is never consulted for it.
				edges[a.resolved] = append(edges[a.resolved], ai)
				continue
			}
			if a.anchoredOK {
				edges[a.anchored] = append(edges[a.anchored], ai)
			}
			for _, id := range g.nodesNamedByEndpoint(a.from) {
				edges[id] = append(edges[id], ai)
			}
		}
		g.callerAttr = attr
		g.callerEdges = edges
	})
	return g.callerAttr
}

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
	attrs := g.callerIndex()
	seen := map[string]bool{}
	var out []string
	for _, ai := range g.callerEdges[target] {
		a := attrs[ai]
		// Caller endpoint: the edge belongs to the target only when the
		// caller resolves to it. A target whose bare name is shared by
		// several packages is attributed package-aware via the callee's
		// package. (Resolution outcomes are memoized per edge by
		// callerIndex, which also files the edge under every node its
		// caller endpoint can name; the target-dependent precedence below
		// still decides the attribution.)
		var fromID string
		switch {
		case a.resolvedOK:
			fromID = a.resolved
		case g.endpointNamesNode(a.from, target):
			// A receiver-qualified caller endpoint naming the target
			// directly ("Server.dispatch" for a Server-method target) is
			// the most specific evidence the endpoint carries — checked
			// before the callee-anchored attribution so a same-package
			// method call cannot be mis-attributed to a same-named method
			// in an importing package.
			fromID = target
		case a.anchoredOK:
			fromID = a.anchored
		default:
			// Unresolvable callee AND ambiguous caller: the edge cannot
			// be attributed to any package — drop it rather than guess.
			continue
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
		// A foreign-qualified callee ("http.DefaultClient.Do") is never
		// resolved to the same-named project symbol (its bare name may be
		// unique; the call is still the stdlib method, not the project's).
		name := a.to
		if node, found := byID[a.to]; found && node.Kind == "reference" {
			name = node.Label
		}
		if !g.foreignCallee(a.to) {
			if toID, ok := g.ResolveEdgeEndpoint(a.to, a.from); ok {
				if n, ok2 := byID[toID]; ok2 && n.Symbol != nil {
					if n.Symbol.Qualified != "" {
						name = n.Symbol.Qualified
					} else {
						name = n.Symbol.Name
					}
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

	// Resolve each affected package path to its module node. Module IDs ARE
	// package paths, so the previous full scan over every node was O(N) per
	// root for a lookup that touches only the affected directories (a hub
	// type expands to 189 roots; measured as the top flat cost of
	// `kern impact` in the 2026-10-28 profile).
	byID := g.nodesByID()
	out := make([]domain.Node, 0, len(affected))
	for dir := range affected {
		if n, ok := byID[dir]; ok && n.Kind == "module" {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
	if s.Kind == "heading" || langparse.IsDocFile(s.File) {
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
	symToks := symbolNameTokens(simple)

	// The scoped covering set (same-package + direct callers), tiered once at
	// its best tier. testsByPackage and incoming are both ID-sorted, so
	// membership is a binary search instead of a per-root map: a hub type's
	// roots share large packages (Server: 189 roots x ~500 same-package
	// tests), and filling a map per root was the report's largest remaining
	// allocation (2026-10-28 profile).
	var pkgIDs []string
	if n.Symbol != nil && n.Symbol.File != "" {
		pkgIDs = g.testsByPackage()[filepath.Dir(n.Symbol.File)]
	}
	callers := incoming[resolved]
	var filePaired, nameMatched, directCallers, samePkg []domain.Node
	tier := func(nn domain.Node, direct bool) {
		var f string
		if nn.Symbol != nil {
			f = nn.Symbol.File
		}
		switch {
		case paired != "" && f == paired:
			filePaired = append(filePaired, nn)
		case g.testNameMatchesSymbol(nn, symToks):
			nameMatched = append(nameMatched, nn)
		case direct:
			directCallers = append(directCallers, nn)
		default:
			samePkg = append(samePkg, nn)
		}
	}
	for _, id := range pkgIDs {
		nn, ok := byID[id]
		if !ok || id == resolved || !isTest(nn.Symbol) {
			continue // a test does not cover itself; only tests cover
		}
		tier(nn, hasID(callers, id))
	}
	last := ""
	for _, id := range callers {
		if id == last {
			continue // incoming can repeat an edge; tier once
		}
		last = id
		if hasID(pkgIDs, id) {
			continue // already tiered from the package bucket
		}
		nn, ok := byID[id]
		if !ok || id == resolved || !isTest(nn.Symbol) {
			continue
		}
		tier(nn, true)
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

// hasID reports whether the ascending, sorted ids contains want.
func hasID(ids []string, want string) bool {
	i := sort.SearchStrings(ids, want)
	return i < len(ids) && ids[i] == want
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
// "TestParseTaintRange" share "taint"). symToks is the symbol's token set
// (computed once per query); the test's own token set is cached per node.
func (g *Graph) testNameMatchesSymbol(nn domain.Node, symToks map[string]bool) bool {
	if len(symToks) == 0 || nn.Symbol == nil || nn.Symbol.Name == "" {
		return false
	}
	return anyTokenShared(g.symbolNameTokens(nn.ID, nn.Symbol.Name), symToks)
}

// symbolNameTokens returns node id's cached name-token set, tokenizing once
// per graph. A hub-type impact asks the same tests about every expanded root.
func (g *Graph) symbolNameTokens(id, name string) map[string]bool {
	if v, ok := g.testTokens.Load(id); ok {
		return v.(map[string]bool)
	}
	toks := symbolNameTokens(name)
	g.testTokens.Store(id, toks)
	return toks
}

func anyTokenShared(a, b map[string]bool) bool {
	for t := range a {
		if b[t] {
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
