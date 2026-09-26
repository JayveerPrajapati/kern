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
	return entitiesForSymbol(g, symbol)
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

// entitiesForSymbol collects the entity nodes connected to the queried
// symbol. A symbol resolves to every candidate node ID (exact ID, qualified
// name, bare name, or Type.Method receiver form) — unlike the code graph's
// single-ID resolver, an ambiguous bare name like "NewServer" (two
// constructors) resolves to ALL candidates so entity connections union
// across same-named symbols. For each candidate the connections are:
//
//   - direct twin edges: the entity nodes connected to the symbol's graph
//     node or its defining file node (after canonical node-ID resolution,
//     so raw handler names like "NewServer" resolve to their graph nodes);
//   - package service: the service entity of the symbol's package (the
//     package as a deployable unit), plus the endpoints that service serves
//     — so a server constructor surfaces the endpoints of the service it
//     belongs to even when no twin edge touches it directly.
//
// The result is deterministic: sorted by (kind, name, direction, edge) with
// exact duplicates collapsed.
func entitiesForSymbol(g *intel.Graph, symbol string) ([]EntityInfo, error) {
	cands := resolveSymbolIDs(g, symbol)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no symbol found: %s", symbol)
	}
	// Entity nodes by ID.
	entities := map[string]domain.Node{}
	for _, n := range g.Nodes {
		if entityKinds[n.Kind] {
			entities[n.ID] = n
		}
	}
	type conn struct {
		info      EntityInfo
		direct    bool // other endpoint is the symbol node (not just its file)
		edge, dir string
	}
	best := map[string]conn{} // entity ID -> preferred connection
	for _, symID := range cands {
		// Code endpoints: the symbol node itself plus its defining file node.
		code := map[string]bool{symID: true}
		for _, n := range g.Nodes {
			if n.ID == symID && n.Symbol != nil && n.Symbol.File != "" {
				code["file:"+n.Symbol.File] = true
			}
		}
		// Walk twin edges; prefer the connection that touches the symbol node
		// itself over one that only touches its file.
		for _, e := range g.Edges {
			ent, codeID, direction, ok := entityConnection(e, code, entities, g)
			if !ok {
				continue
			}
			prev, seen := best[ent.ID]
			info := entityInfo(ent)
			info.Direction = direction
			info.Edge = e.Kind
			direct := codeID == symID
			// Prefer the connection touching the symbol node itself; tie-break
			// on the edge kind so the winner is deterministic.
			if !seen || direct && !prev.direct || direct == prev.direct && e.Kind < prev.edge {
				best[ent.ID] = conn{info: info, direct: direct, edge: e.Kind, dir: direction}
			}
		}
		// Package service: the symbol's package hosts it, and the service
		// serves the package's endpoints.
		if pkg := packageOf(g, symID); pkg != "" {
			svcID := "service:pkg:" + ids.Escape(pkg)
			if svc, ok := entities[svcID]; ok {
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
	for _, e := range g.Edges {
		if e.Kind != "serves" {
			continue
		}
		if _, ok := best[e.From]; !ok {
			continue
		}
		api, ok := entities[e.To]
		if !ok {
			continue
		}
		if _, seen := best[e.To]; seen {
			continue
		}
		info := entityInfo(api)
		info.Direction = "entity->code"
		info.Edge = "serves"
		best[e.To] = conn{info: info, direct: true, edge: "serves", dir: "entity->code"}
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

// resolveSymbolIDs maps a user-provided symbol reference to every candidate
// symbol node ID in the graph: an exact node-ID match, a qualified-name
// match, a bare-name match, or a Type.Method receiver match. Unlike the
// code graph's single-ID resolver, an ambiguous bare name resolves to ALL
// matching nodes so same-named symbols union their entity connections
// instead of one arbitrarily winning.
func resolveSymbolIDs(g *intel.Graph, symbol string) []string {
	seen := map[string]bool{}
	var out []string
	bare := symbol
	receiver, method := "", ""
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
		receiver, method = symbol[:i], symbol[i+1:]
	}
	for _, n := range g.Nodes {
		if n.Symbol == nil {
			continue
		}
		match := n.ID == symbol ||
			n.Symbol.Qualified == symbol ||
			n.Symbol.Name == bare ||
			(receiver != "" && n.Symbol.Receiver == receiver && n.Symbol.Name == method)
		if !match || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n.ID)
	}
	return out
}

// packageOf derives the package path of a symbol node from its node ID:
// node IDs are "<pkg>.<Qualified>", so stripping the known Qualified suffix
// recovers the package (root-package symbols have no prefix and return "").
func packageOf(g *intel.Graph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id && n.Symbol != nil && n.Symbol.Qualified != "" {
			if pkg := strings.TrimSuffix(id, "."+n.Symbol.Qualified); pkg != id {
				return pkg
			}
		}
	}
	return ""
}

// entityConnection inspects one edge: when exactly one endpoint is an entity
// node and the other is a code endpoint (the queried symbol's node or file),
// it returns the entity node, the code endpoint ID, the connection direction
// ("entity->code" when the entity is the edge's From, "code->entity"
// otherwise), and true.
func entityConnection(e domain.Edge, code map[string]bool, entities map[string]domain.Node, g *intel.Graph) (domain.Node, string, string, bool) {
	entFrom, fromIsEntity := entities[e.From]
	if fromIsEntity {
		if codeID, ok := codeEndpoint(g, e.To, code, entities); ok {
			return entFrom, codeID, "entity->code", true
		}
		return domain.Node{}, "", "", false
	}
	entTo, toIsEntity := entities[e.To]
	if toIsEntity {
		if codeID, ok := codeEndpoint(g, e.From, code, entities); ok {
			return entTo, codeID, "code->entity", true
		}
	}
	return domain.Node{}, "", "", false
}

// codeEndpoint reports whether ref is a code endpoint (after canonical
// resolution). Entity IDs are never code endpoints.
func codeEndpoint(g *intel.Graph, ref string, code map[string]bool, entities map[string]domain.Node) (string, bool) {
	if code[ref] {
		return ref, true
	}
	if _, isEnt := entities[ref]; isEnt {
		return "", false
	}
	if id, ok := g.ResolveNodeID(ref); ok {
		if code[id] {
			return id, true
		}
		if _, isEnt := entities[id]; isEnt {
			return "", false
		}
	}
	return "", false
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
