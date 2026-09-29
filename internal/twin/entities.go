// Entity-level knowledge graph (Feature Batch E): deterministic rendering
// of the twin's entity nodes — API endpoints, databases, tables, topics,
// services, and deployments — connected to a queried code symbol (or the
// repo-wide entity inventory when no symbol is given). Shared by `kern graph
// --entities` (CLI) and kern_graph entities=true (MCP). No LLM, no network:
// everything is derived from the merged knowledge graph.

package twin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// entityKinds is the set of entity (non-code) node kinds the twin extractors
// emit. Runtime observability nodes (error, service-health) are excluded:
// they describe transient telemetry, not domain entities, and surfacing every
// error event as an "entity" would drown the inventory in noise.
var entityKinds = map[string]bool{
	"api":        true,
	"database":   true,
	"table":      true,
	"topic":      true,
	"service":    true,
	"deployment": true,
}

// EntityInfo is one entity in the entity list. Direction and Edge are set in
// symbol mode (how the entity connects to the queried code node); inventory
// mode leaves them empty. Version/CommitSHA are set for deployment entities
// when the runtime source provided them.
type EntityInfo struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Direction string `json:"direction,omitempty"`  // "entity->code" or "code->entity"
	Edge      string `json:"edge,omitempty"`       // twin edge kind connecting to the code node
	Version   string `json:"version,omitempty"`    // deployment version, when present
	CommitSHA string `json:"commit_sha,omitempty"` // deployment commit SHA, when present
}

// MergeIntoIndex merges the twin extractors into a code graph derived from
// the given index, mirroring platform.go's wiring (runtime source included),
// then augments the result with entities derived from the index's own
// framework entry-point metadata (indexEntities). It returns the merged
// knowledge graph the entity renderers consume. The runtime source is
// resolved the same way the app platform resolves it, so a
// `.kern/runtime.json` (or a live adapter) feeds deployment/error nodes; a
// nil source leaves those kinds at zero instances.
func MergeIntoIndex(ix *index.Index, root string) *intel.Graph {
	g := intel.FromIndex(ix)
	// Best-effort, like platform.go: extractor errors are non-fatal.
	_ = Merge(&g, NewExtractors(root, runtime.LoadSource(root)))
	indexEntities(ix, &g)
	return &g
}

// Entities returns the entity list for a symbol (the entities the code node
// references or is referenced by via twin edges), or the repo-wide entity
// inventory grouped by kind when symbol is "". The result is deterministic:
// sorted by (kind, name, direction, edge). An unresolvable symbol returns an
// error.
func Entities(g *intel.Graph, symbol string) ([]EntityInfo, error) {
	if symbol == "" {
		return inventory(g), nil
	}
	if g == nil {
		return nil, fmt.Errorf("no symbol found: %s", symbol)
	}
	return buildEntityGraph(g).entitiesForSymbol(symbol)
}

// EntitiesMany returns the entity list for every symbol in symbols, in the
// same order. It builds the per-graph entity index once and answers each
// symbol against it, so a large symbol set — the impact overlay feeds every
// affected symbol of a hub, thousands for a core symbol — costs one graph
// scan plus per-symbol connection lookups instead of one full graph scan per
// symbol (the pre-index Entities path was the dominant cost of `kern impact`,
// tens of seconds for a 130-caller hub). Unresolvable symbols yield nil
// entries; callers treat them like the per-symbol error path (skip).
func EntitiesMany(g *intel.Graph, symbols []string) [][]EntityInfo {
	out := make([][]EntityInfo, len(symbols))
	if g == nil {
		return out
	}
	eg := buildEntityGraph(g)
	for i, sym := range symbols {
		ents, err := eg.entitiesForSymbol(sym)
		if err != nil {
			continue // unresolvable symbol — same skip as the per-symbol error path
		}
		out[i] = ents
	}
	return out
}

// RenderEntities renders a deterministic text block for an entity list:
//
//	entities (2):
//	  api        GET /users        entity->code  implements
//	  deployment api v1.2.3        entity->code  deploys
//	    (version=v1.2.3 commit=abc1234)
//
// Inventory lists (entries without a direction, from Entities with symbol "")
// render grouped by kind with per-kind counts:
//
//	entities (4):
//	  api (1):
//	    GET /users
//	  deployment (1):
//	    api v1.2.3
//	    (version=v1.2.3 commit=abc1234)
func RenderEntities(ents []EntityInfo) string {
	if len(ents) == 0 {
		return "entities (0):"
	}
	if ents[0].Direction == "" {
		return renderEntityInventory(ents)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "entities (%d):\n", len(ents))
	for _, e := range ents {
		fmt.Fprintf(&b, "  %-12s %-28s %-12s %s\n", e.Kind, e.Name, e.Direction, e.Edge)
		if e.Version != "" || e.CommitSHA != "" {
			fmt.Fprintf(&b, "    (%s)\n", deploymentAttrs(e))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// deploymentAttrs renders the deployment version/commit attribute suffix.
func deploymentAttrs(e EntityInfo) string {
	attrs := ""
	if e.Version != "" {
		attrs = "version=" + e.Version
	}
	if e.CommitSHA != "" {
		if attrs != "" {
			attrs += " "
		}
		attrs += "commit=" + e.CommitSHA
	}
	return attrs
}

// renderEntityInventory renders the no-symbol inventory grouped by kind.
// The input must already be sorted by (kind, name).
func renderEntityInventory(ents []EntityInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "entities (%d):\n", len(ents))
	i := 0
	for i < len(ents) {
		kind := ents[i].Kind
		j := i
		for j < len(ents) && ents[j].Kind == kind {
			j++
		}
		fmt.Fprintf(&b, "  %s (%d):\n", kind, j-i)
		for _, e := range ents[i:j] {
			fmt.Fprintf(&b, "    %s\n", e.Name)
			if e.Version != "" || e.CommitSHA != "" {
				fmt.Fprintf(&b, "      (%s)\n", deploymentAttrs(e))
			}
		}
		i = j
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// inventory returns every entity node, sorted by (kind, name), for the
// no-symbol entity inventory. Identical endpoints are collapsed: the api
// extractor can emit the same route once per matching framework pattern
// (e.g. api:gin:GET:/health and api:fastapi:GET:/health), and the index
// derivation adds its own api:index:... nodes for the same routes, so a
// route is rendered once regardless of how many extractors or frameworks
// recorded it.
func inventory(g *intel.Graph) []EntityInfo {
	var out []EntityInfo
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if !entityKinds[n.Kind] {
			continue
		}
		info := entityInfo(n)
		key := info.Kind + "\x00" + info.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// entityGraph is the precomputed, read-only index over a merged knowledge
// graph that backs symbol→entity queries. Building it is O(nodes+edges);
// after that each symbol query touches only the queried symbol's own
// connections instead of re-scanning the whole graph. This matters for the
// impact overlay, which feeds every affected symbol of a hub (thousands) to
// the entity render: the per-symbol full-graph scan was the dominant cost of
// `kern impact` (tens of seconds for a 130-caller hub). The graph is read-only
// after construction, so the index is built once per query batch and reused.
type entityGraph struct {
	// entities maps entity node ID → node (entity kinds only).
	entities map[string]domain.Node
	// codeNodes is the set of non-entity node IDs (symbols, files, modules).
	codeNodes map[string]bool
	// byID maps symbol node ID → position in g.Nodes, for exact-ID candidate
	// resolution. The other candidate indexes key the same positions so
	// candidates() can reproduce resolveSymbolIDs' single ordered scan.
	byID      map[string]int
	qualIndex map[string][]int  // symbol Qualified → positions
	nameIndex map[string][]int  // symbol Name → positions
	recvIndex map[string][]int  // "Receiver.Method" → positions
	nodeIDs   []string          // position → node ID (parallel to g.Nodes)
	symFile   map[string]string // symbol node ID → defining file
	pkgOf     map[string]string // symbol node ID → package path
	// codeConns maps a code node ID (symbol node or "file:<path>" node) to the
	// entity connections touching it, recorded from the graph's twin edges.
	codeConns map[string][]entityConn
	// servesEdges preserves the graph's "serves" edges in original order so
	// the served-endpoint expansion is byte-identical to the pre-index walk.
	servesEdges []struct{ from, to string }
}

// entityConn is one entity connection recorded against a code node.
type entityConn struct {
	entID string
	edge  string
	dir   string
}

// buildEntityGraph indexes a merged graph for symbol→entity queries: the
// entity node set, the candidate-resolution indexes, each code node's entity
// connections, and the service→endpoint "serves" edges.
func buildEntityGraph(g *intel.Graph) *entityGraph {
	eg := &entityGraph{
		entities:  map[string]domain.Node{},
		codeNodes: map[string]bool{},
		byID:      map[string]int{},
		qualIndex: map[string][]int{},
		nameIndex: map[string][]int{},
		recvIndex: map[string][]int{},
		symFile:   map[string]string{},
		pkgOf:     map[string]string{},
		codeConns: map[string][]entityConn{},
	}
	for i, n := range g.Nodes {
		eg.nodeIDs = append(eg.nodeIDs, n.ID)
		if entityKinds[n.Kind] {
			eg.entities[n.ID] = n
			continue // entity nodes are never code endpoints
		}
		eg.codeNodes[n.ID] = true
		if n.Symbol == nil {
			continue
		}
		eg.byID[n.ID] = i
		if n.Symbol.Qualified != "" {
			eg.qualIndex[n.Symbol.Qualified] = append(eg.qualIndex[n.Symbol.Qualified], i)
			// packageOf: node IDs are "<pkg>.<Qualified>"; stripping the known
			// Qualified suffix recovers the package path (root-package symbols
			// have no prefix and get no entry).
			if pkg := strings.TrimSuffix(n.ID, "."+n.Symbol.Qualified); pkg != n.ID {
				eg.pkgOf[n.ID] = pkg
			}
		}
		if n.Symbol.Name != "" {
			eg.nameIndex[n.Symbol.Name] = append(eg.nameIndex[n.Symbol.Name], i)
		}
		if n.Symbol.Receiver != "" && n.Symbol.Name != "" {
			eg.recvIndex[n.Symbol.Receiver+"."+n.Symbol.Name] = append(eg.recvIndex[n.Symbol.Receiver+"."+n.Symbol.Name], i)
		}
		if n.Symbol.File != "" {
			eg.symFile[n.ID] = n.Symbol.File
			// A symbol's defining file is a code endpoint for that symbol even
			// when the graph carries no explicit "file:<path>" node (the old
			// per-symbol code set added "file:"+File unconditionally), so the
			// file key must be resolvable here too.
			eg.codeNodes["file:"+n.Symbol.File] = true
		}
	}
	// Entity connections: for each edge with exactly one entity endpoint,
	// resolve the other endpoint to a code node ID and record the connection
	// under it. Mirrors the old per-symbol entityConnection/codeEndpoint walk,
	// but once per graph instead of once per queried symbol.
	for _, e := range g.Edges {
		ent, fromIsEntity := eg.entities[e.From]
		if fromIsEntity {
			if codeID, ok := eg.codeEndpoint(g, e.To); ok {
				eg.codeConns[codeID] = append(eg.codeConns[codeID], entityConn{entID: ent.ID, edge: e.Kind, dir: "entity->code"})
			}
			continue
		}
		ent, toIsEntity := eg.entities[e.To]
		if toIsEntity {
			if codeID, ok := eg.codeEndpoint(g, e.From); ok {
				eg.codeConns[codeID] = append(eg.codeConns[codeID], entityConn{entID: ent.ID, edge: e.Kind, dir: "code->entity"})
			}
		}
	}
	// Served-endpoint expansion edges, kept in original order.
	for _, e := range g.Edges {
		if e.Kind != "serves" {
			continue
		}
		if _, ok := eg.entities[e.To]; !ok {
			continue
		}
		eg.servesEdges = append(eg.servesEdges, struct{ from, to string }{e.From, e.To})
	}
	return eg
}

// codeEndpoint is the universal form of the old per-symbol codeEndpoint: the
// "code" set is every non-entity node, so an edge endpoint resolves to the
// code node it touches and per-symbol lookups later pick out the connections
// for the queried symbol's own node and file.
func (eg *entityGraph) codeEndpoint(g *intel.Graph, ref string) (string, bool) {
	if eg.codeNodes[ref] {
		return ref, true
	}
	if _, isEnt := eg.entities[ref]; isEnt {
		return "", false
	}
	if id, ok := g.ResolveNodeID(ref); ok {
		if eg.codeNodes[id] {
			return id, true
		}
		if _, isEnt := eg.entities[id]; isEnt {
			return "", false
		}
	}
	return "", false
}

// candidates maps a user-provided symbol reference to every candidate symbol
// node ID, in graph node order, exactly like the old resolveSymbolIDs single
// scan: an exact node-ID match, a qualified-name match, a bare-name match, or
// a "Type.Method" receiver match. An ambiguous bare name resolves to ALL
// matching nodes so same-named symbols union their entity connections.
func (eg *entityGraph) candidates(symbol string) []string {
	bare := symbol
	receiver := ""
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
		receiver = symbol[:i]
	}
	pos := map[int]bool{}
	add := func(ids []int) {
		for _, p := range ids {
			pos[p] = true
		}
	}
	if p, ok := eg.byID[symbol]; ok {
		pos[p] = true
	}
	add(eg.qualIndex[symbol])
	add(eg.nameIndex[bare])
	if receiver != "" {
		add(eg.recvIndex[symbol]) // "Receiver.Method"
	}
	positions := make([]int, 0, len(pos))
	for p := range pos {
		positions = append(positions, p)
	}
	sort.Ints(positions)
	out := make([]string, 0, len(positions))
	for _, p := range positions {
		out = append(out, eg.nodeIDs[p])
	}
	return out
}

// entitiesForSymbol collects the entity nodes connected to the queried
// symbol against the precomputed index. A symbol resolves to every candidate
// node ID (exact ID, qualified name, bare name, or Type.Method receiver form)
// so entity connections union across same-named symbols. For each candidate
// the connections are:
//
//   - direct twin edges: the entity nodes connected to the symbol's graph
//     node or its defining file node;
//   - package service: the service entity of the symbol's package (the
//     package as a deployable unit), plus the endpoints that service serves.
//
// The result is deterministic: sorted by (kind, name, direction, edge) with
// exact duplicates collapsed. It is byte-identical to the pre-index
// implementation (which re-scanned the whole graph per symbol).
func (eg *entityGraph) entitiesForSymbol(symbol string) ([]EntityInfo, error) {
	cands := eg.candidates(symbol)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no symbol found: %s", symbol)
	}
	type conn struct {
		info      EntityInfo
		direct    bool // other endpoint is the symbol node (not just its file)
		edge, dir string
	}
	best := map[string]conn{} // entity ID -> preferred connection
	update := func(entID, edge, dir string, direct bool) {
		ent, ok := eg.entities[entID]
		if !ok {
			return
		}
		prev, seen := best[entID]
		info := entityInfo(ent)
		info.Direction = dir
		info.Edge = edge
		// Prefer the connection touching the symbol node itself; tie-break on
		// the edge kind so the winner is deterministic (same fold as the
		// pre-index per-symbol edge walk; order-independent because the winner
		// is min-edge-kind within the preferred directness class).
		if !seen || direct && !prev.direct || direct == prev.direct && edge < prev.edge {
			best[entID] = conn{info: info, direct: direct, edge: edge, dir: dir}
		}
	}
	for _, symID := range cands {
		// Connections touching the symbol node itself (direct) and its
		// defining file node (indirect, codeID != symID).
		for _, c := range eg.codeConns[symID] {
			update(c.entID, c.edge, c.dir, true)
		}
		if file := eg.symFile[symID]; file != "" {
			for _, c := range eg.codeConns["file:"+file] {
				update(c.entID, c.edge, c.dir, false)
			}
		}
		// Package service: the symbol's package hosts it.
		if pkg := eg.pkgOf[symID]; pkg != "" {
			svcID := "service:pkg:" + ids.Escape(pkg)
			if svc, ok := eg.entities[svcID]; ok {
				if _, seen := best[svcID]; !seen {
					info := entityInfo(svc)
					info.Direction = "code->entity"
					info.Edge = "hosts"
					best[svcID] = conn{info: info, direct: true, edge: "hosts", dir: "code->entity"}
				}
			}
		}
	}
	// Expand package services into the endpoints they serve, so a symbol
	// connected to the service also surfaces the endpoints of that service.
	// Serves edges are walked in original graph order (a service added by an
	// earlier edge can serve endpoints picked up by later edges, exactly like
	// the pre-index walk).
	for _, se := range eg.servesEdges {
		if _, ok := best[se.from]; !ok {
			continue
		}
		if _, seen := best[se.to]; seen {
			continue
		}
		api, ok := eg.entities[se.to]
		if !ok {
			continue
		}
		info := entityInfo(api)
		info.Direction = "entity->code"
		info.Edge = "serves"
		best[se.to] = conn{info: info, direct: true, edge: "serves", dir: "entity->code"}
	}
	// Deterministic output with exact-duplicate collapse: the same endpoint
	// can reach a symbol through several routes (extractor api node, index
	// api node, service expansion) with identical (kind, name, direction,
	// edge) — keep one.
	out := make([]EntityInfo, 0, len(best))
	for _, c := range best {
		out = append(out, c.info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Direction != out[j].Direction {
			return out[i].Direction < out[j].Direction
		}
		return out[i].Edge < out[j].Edge
	})
	deduped := out[:0]
	prev := EntityInfo{}
	for i, e := range out {
		if i > 0 && e.Kind == prev.Kind && e.Name == prev.Name &&
			e.Direction == prev.Direction && e.Edge == prev.Edge {
			continue
		}
		deduped = append(deduped, e)
		prev = e
	}
	return deduped, nil
}

// entityInfo builds the entity descriptor for a node, attaching deployment
// version/commit attributes when the node carries them.
func entityInfo(n domain.Node) EntityInfo {
	info := EntityInfo{Kind: n.Kind, Name: n.Label}
	if info.Name == "" {
		info.Name = n.ID
	}
	if n.Kind == "deployment" && n.Deployment != nil {
		info.Version = n.Deployment.Version
		info.CommitSHA = n.Deployment.CommitSHA
	}
	return info
}
