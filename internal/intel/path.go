package intel

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Resolve maps a user-supplied name (simple "greet", qualified "Type.Method",
// raw call form "pkg.Fn") to a canonical in-project symbol FullName. Exact
// symbol matches win; a simple name that is unambiguous also resolves.
func Resolve(ix *index.Index, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if symbolByName(ix, name) {
		return name, true
	}
	// Package-path-qualified ("internal/intel/ReadIndex"): try the tail.
	if i := strings.LastIndex(name, "/"); i >= 0 {
		if c, ok := Resolve(ix, name[i+1:]); ok {
			return c, true
		}
	}
	// Raw callee form ("index.Build"): the trailing part is the symbol.
	if s := simpleName(name); s != name && symbolByName(ix, s) {
		return s, true
	}
	// Simple name: resolve only when unambiguous.
	var matches []string
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			matches = append(matches, s.FullName())
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

func symbolByName(ix *index.Index, full string) bool {
	for _, s := range ix.Symbols {
		if s.FullName() == full {
			return true
		}
	}
	return false
}

// ShortestPath returns the shortest chain of in-project symbols connecting
// from to to through call edges (followed in either direction), or nil when
// the two are not connected. The path is deterministic and hops stay within
// the project-local call graph.
func ShortestPath(ix *index.Index, from, to string) []string {
	if from == "" || to == "" {
		return nil
	}
	adj := map[string][]string{}
	for _, s := range ix.Symbols {
		caller := s.FullName()
		for _, c := range localCallees(ix, caller) {
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
	if from == to {
		return []string{from}
	}
	prev := map[string]string{}
	visited := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if visited[nb] {
				continue
			}
			visited[nb] = true
			prev[nb] = cur
			if nb == to {
				return rebuildPath(prev, from, to)
			}
			queue = append(queue, nb)
		}
	}
	return nil
}

func rebuildPath(prev map[string]string, from, to string) []string {
	var rev []string
	for cur := to; cur != ""; cur = prev[cur] {
		rev = append(rev, cur)
		if cur == from {
			break
		}
	}
	path := make([]string, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		path = append(path, rev[i])
	}
	return path
}

// PathHop describes one step of a resolved path with its source location.
type PathHop struct {
	Symbol string `json:"symbol"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// RenderPath returns a compact chain like "A -> B -> C" with file:line for
// every hop.
func RenderPath(ix *index.Index, path []string) string {
	if len(path) == 0 {
		return "no path found (symbols are not connected through project-local calls)"
	}
	loc := map[string]string{}
	for _, s := range ix.Symbols {
		if _, ok := loc[s.FullName()]; !ok {
			loc[s.FullName()] = fmt.Sprintf("%s:%d", s.File, s.Line)
		}
	}
	var b strings.Builder
	for i, hop := range path {
		if i == 0 {
			b.WriteString(hop)
		} else {
			b.WriteString("\n   \u2192 ")
			b.WriteString(hop)
		}
		if l := loc[hop]; l != "" {
			b.WriteString("  ")
			b.WriteString(l)
		}
	}
	return b.String()
}
