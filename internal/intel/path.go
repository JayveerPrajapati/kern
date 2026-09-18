package intel

import (
	"container/heap"
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
	// Method-qualified ("Type.Method" or "pkg.Type.Method"): resolve against
	// receiver names, not bare method names, so an overloaded or
	// package-qualified receiver never degrades to a random same-named method
	// (or zero results) when the exact FullName is not in the index.
	if dot := strings.LastIndex(name, "."); dot >= 0 && dot+1 < len(name) {
		if matches := ix.ResolveDottedMethod(name[:dot], name[dot+1:]); len(matches) > 0 {
			return matches[0].FullName(), true
		}
	}
	// Raw callee form ("index.Build"): the trailing part is the symbol.
	if s := simpleName(name); s != name && symbolByName(ix, s) {
		return s, true
	}
	// Simple name: resolve when unambiguous; when ambiguous, return the
	// first match so callers (near, path, probe) still work. The user can
	// disambiguate with a qualified name ("Type.Method") for precision.
	var matches []string
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			matches = append(matches, s.FullName())
		}
	}
	if len(matches) >= 1 {
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
	return ShortestPathMin(ix, from, to, "")
}

// ShortestPathMin is ShortestPath with a minimum-confidence filter: edges
// whose provenance label ranks below the threshold (see MinConfidenceFilter)
// are excluded from the search graph, so a returned path never hops through
// AMBIGUOUS phantom references. An empty threshold searches every edge.
func ShortestPathMin(ix *index.Index, from, to string, minConf string) []string {
	if from == "" || to == "" {
		return nil
	}
	if from == to {
		return []string{from}
	}
	passes := MinConfidenceFilter(minConf)
	adj := map[string][]string{}
	names := canonicalNames(ix)
	local := localNames(ix)
	// One-pass confidence index (caller -> normalized callee -> label). The
	// previous per-edge path went through EdgeConfidenceLabel ->
	// canonicalSimple -> Resolve -> symbolByName, a linear scan of every
	// symbol per unresolved edge — the profile showed 90% of `kern path`
	// time (~43s on 13k symbols) inside Resolve. Recorded targets are keyed
	// under their raw, simple, and canonical forms so the lookup below is
	// O(1); unrecorded edges stay AMBIGUOUS (conservative, same default as
	// the old heuristic's miss path).
	edgeConf := map[string]map[string]string{}
	addConf := func(caller, callee, label string) {
		if caller == "" || callee == "" || caller == callee {
			return
		}
		m := edgeConf[caller]
		if m == nil {
			m = map[string]string{}
			edgeConf[caller] = m
		}
		if _, ok := m[callee]; !ok {
			m[callee] = label
		}
	}
	for caller, ces := range ix.Calls {
		for _, ce := range ces {
			lbl := labelOf(ce.Confidence.String())
			addConf(caller, ce.Target, lbl)
			addConf(caller, simpleName(ce.Target), lbl)
			addConf(caller, canon(names, ce.Target), lbl)
		}
	}
	labelFor := func(from, to string) string {
		if m := edgeConf[from]; m != nil {
			if l, ok := m[to]; ok {
				return l
			}
		}
		if m := edgeConf[to]; m != nil {
			if l, ok := m[from]; ok {
				return l
			}
		}
		return confAmbiguous
	}
	for _, s := range ix.Symbols {
		caller := s.FullName()
		for _, c := range localCalleesWith(ix, caller, local) {
			c = canon(names, c) // receiver-instance form -> canonical method FullName
			if c == caller {
				continue
			}
			if !passes(labelFor(caller, c)) {
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

	// Min-heap priority queue. The previous implementation re-scanned the
	// whole queue per extraction (O(V^2) on 13k+ nodes) — one of the two
	// quadratic behaviours that made `kern path` take ~55s.
	dist := map[string]int{from: 0}
	prev := map[string]string{}
	visited := map[string]bool{}
	pq := &pathPQ{{node: from, dist: 0}}
	heap.Init(pq)

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(queueItem)
		if visited[cur.node] {
			continue
		}
		visited[cur.node] = true

		if cur.node == to {
			return rebuildPath(prev, from, to)
		}

		for _, nb := range adj[cur.node] {
			if visited[nb] {
				continue
			}
			cost := edgeWeight(labelFor(cur.node, nb))
			newDist := cur.dist + cost
			if d, ok := dist[nb]; !ok || newDist < d {
				dist[nb] = newDist
				prev[nb] = cur.node
				heap.Push(pq, queueItem{node: nb, dist: newDist})
			}
		}
	}
	return nil
}

type queueItem struct {
	node string
	dist int
}

type pathPQ []queueItem

// Len implements heap.Interface.
func (p pathPQ) Len() int { return len(p) }

// Less implements heap.Interface (min-heap by distance).
func (p pathPQ) Less(i, j int) bool { return p[i].dist < p[j].dist }

// Swap implements heap.Interface.
func (p pathPQ) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

// Push implements heap.Interface.
func (p *pathPQ) Push(x any) { *p = append(*p, x.(queueItem)) }

// Pop implements heap.Interface.
func (p *pathPQ) Pop() any {
	old := *p
	n := len(old)
	it := old[n-1]
	*p = old[:n-1]
	return it
}

func edgeWeight(label string) int {
	switch label {
	case confExtracted:
		return 1
	case confInferred:
		return 10
	default:
		return 25
	}
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

func findSymbolLoc(ix *index.Index, hop string, adjacent ...string) string {
	var candidates []index.Symbol
	for _, s := range ix.Symbols {
		if s.FullName() == hop || s.Name == hop {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return fmt.Sprintf("%s:%d", candidates[0].File, candidates[0].Line)
	}
	for _, cand := range candidates {
		for _, ce := range ix.Calls[cand.FullName()] {
			for _, adj := range adjacent {
				if ce.Target == adj || simpleName(ce.Target) == simpleName(adj) {
					return fmt.Sprintf("%s:%d", cand.File, cand.Line)
				}
			}
		}
		for _, caller := range ix.Callers[cand.FullName()] {
			for _, adj := range adjacent {
				if caller == adj || simpleName(caller) == simpleName(adj) {
					return fmt.Sprintf("%s:%d", cand.File, cand.Line)
				}
			}
		}
	}
	return fmt.Sprintf("%s:%d", candidates[0].File, candidates[0].Line)
}

// RenderPath returns a compact chain like "A -> B -> C" with file:line and
// the provenance label of every hop's edge ([EXTRACTED]/[INFERRED]/
// [AMBIGUOUS]), so each step of the answer is FACT/INFERENCE-classifiable.
func RenderPath(ix *index.Index, path []string) string {
	if len(path) == 0 {
		return "no path found (symbols are not connected through project-local calls)"
	}
	var b strings.Builder
	for i, hop := range path {
		var adjacent []string
		if i > 0 {
			adjacent = append(adjacent, path[i-1])
		}
		if i < len(path)-1 {
			adjacent = append(adjacent, path[i+1])
		}

		if i == 0 {
			b.WriteString(hop)
		} else {
			prev := path[i-1]
			b.WriteString("\n   \u2192 ")
			b.WriteString(hop)
			if label := EdgeConfidenceLabel(ix, prev, hop); label != "" {
				b.WriteString(" [" + label + "]")
			}
			if synth := EdgeSynthLabel(ix, prev, hop); synth != "" {
				b.WriteString(" (SYNTHESIZED: " + synth + ")")
			}
		}
		if l := findSymbolLoc(ix, hop, adjacent...); l != "" {
			b.WriteString("  ")
			b.WriteString(l)
		}
	}
	return b.String()
}
