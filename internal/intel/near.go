package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// NearNode is one symbol in a neighbourhood expansion, linked back to the
// parent it was reached from. Dir is the relationship to the parent:
// "caller" when this node calls the parent, "callee" when the parent calls
// this node.
type NearNode struct {
	Symbol string `json:"symbol"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Depth  int    `json:"depth"`
	Parent string `json:"parent,omitempty"`
	Dir    string `json:"dir,omitempty"`
}

// Near expands the call graph outwards from root, in both directions (callers
// and callees), up to depth hops. It is the dependency-free equivalent of a
// "walk the graph N degrees away" primitive: one call replaces a blind grep or
// a directory dump. maxNodes caps the returned expansion (deterministic BFS
// order, then symbol order within a hop).
func Near(ix *index.Index, root string, depth, maxNodes int) ([]NearNode, error) {
	if root == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	resolved, ok := Resolve(ix, root)
	if !ok {
		return nil, fmt.Errorf("unknown symbol: %s", root)
	}
	if depth < 0 {
		depth = 0
	}
	if maxNodes <= 0 {
		maxNodes = 100
	}

	adj := map[string][]string{}
	names := canonicalNames(ix)
	local := localNames(ix)
	for _, s := range ix.Symbols {
		caller := s.FullName()
		for _, c := range localCalleesWith(ix, caller, local) {
			c = canon(names, c) // receiver-instance form -> canonical method FullName
			if c == caller {
				continue
			}
			if !contains(adj[caller], c) {
				adj[caller] = append(adj[caller], c)
			}
			if !contains(adj[c], caller) {
				adj[c] = append(adj[c], caller)
			}
		}
	}

	type frontier struct {
		symbol string
		depth  int
	}
	// BFS keeps the expansion close to root and deterministic.
	nodes := []NearNode{{Symbol: resolved, Depth: 0}}
	seen := map[string]bool{resolved: true}
	queue := []frontier{{symbol: resolved, depth: 0}}
	for len(queue) > 0 && len(nodes) < maxNodes {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= depth {
			continue
		}
		neighbors := adj[cur.symbol]
		sort.Strings(neighbors)
		for _, nb := range neighbors {
			if seen[nb] {
				continue
			}
			seen[nb] = true
			nodes = append(nodes, NearNode{
				Symbol: nb,
				Depth:  cur.depth + 1,
				Parent: cur.symbol,
				Dir:    relDir(ix, names, cur.symbol, nb),
			})
			if len(nodes) >= maxNodes {
				break
			}
			queue = append(queue, frontier{symbol: nb, depth: cur.depth + 1})
		}
	}

	// Attach kind/file/line for every node.
	meta := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
	}
	for i := range nodes {
		if s, ok := meta[nodes[i].Symbol]; ok {
			nodes[i].Kind = s.Kind
			nodes[i].File = s.File
			nodes[i].Line = s.Line
		}
	}
	return nodes, nil
}

// relDir reports how nb relates to parent: "caller" when nb calls parent,
// "callee" when parent calls nb. names is the precomputed canonical-name map
// (hoisted out of the BFS loop — canonicalNames is O(V) per call).
func relDir(ix *index.Index, names map[string]string, parent, nb string) string {
	for _, c := range ix.Callers[parent] {
		if canon(names, c) == nb {
			return "caller"
		}
	}
	return "callee"
}

// RenderNear renders the expansion as an indented dependency tree, one hop per
// level. Each child is prefixed with an arrow: ↑ means the child calls its
// parent, ↓ means the parent calls the child.
func RenderNear(ix *index.Index, nodes []NearNode) string {
	if len(nodes) == 0 {
		return "no expansion"
	}
	children := map[string][]NearNode{}
	for _, n := range nodes[1:] {
		children[n.Parent] = append(children[n.Parent], n)
	}
	loc := map[string]string{}
	for _, s := range ix.Symbols {
		if _, ok := loc[s.FullName()]; !ok {
			loc[s.FullName()] = fmt.Sprintf("%s:%d", s.File, s.Line)
		}
	}

	var b strings.Builder
	root := nodes[0]
	fmt.Fprintf(&b, "%s", root.Symbol)
	if l := loc[root.Symbol]; l != "" {
		b.WriteString("  ")
		b.WriteString(l)
	}
	b.WriteString("\n")
	renderBranch(&b, children, loc, root.Symbol, 1)
	return strings.TrimSuffix(b.String(), "\n")
}

func renderBranch(b *strings.Builder, children map[string][]NearNode, loc map[string]string, parent string, indent int) {
	kids := children[parent]
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].Dir != kids[j].Dir {
			return kids[i].Dir < kids[j].Dir
		}
		return kids[i].Symbol < kids[j].Symbol
	})
	for _, k := range kids {
		arrow := "↓"
		if k.Dir == "caller" {
			arrow = "↑"
		}
		b.WriteString(strings.Repeat("  ", indent))
		b.WriteString(arrow)
		b.WriteString(" ")
		b.WriteString(k.Symbol)
		if l := loc[k.Symbol]; l != "" {
			b.WriteString("  ")
			b.WriteString(l)
		}
		b.WriteString("\n")
		if len(children[k.Symbol]) > 0 {
			renderBranch(b, children, loc, k.Symbol, indent+1)
		}
	}
}
