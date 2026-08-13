package index

import "sort"

const (
	maxCommunityIterations = 30
	minCommunitySize       = 2
	MaxCommunitySymbols    = 50000
)

// CommunityLabels clusters symbols with label propagation over project-local
// call edges (stdlib/external callees are ignored). The result is
// deterministic: labels are initialised to the sorted symbol name and updated
// in a fixed order with lexicographic tie-breaking. Keys are symbol full
// names; each value is the community label (a member's full name). This is
// the single source of truth for the clustering: the intel package renders
// it into Community reports and the SQLite store persists it, so graph
// consumers never recompute the propagation twice.
// For large repos (> MaxCommunitySymbols), returns empty to avoid O(n*iter) latency.
func (ix *Index) CommunityLabels() map[string]string {
	if len(ix.Symbols) > MaxCommunitySymbols {
		return map[string]string{}
	}
	adj := map[string][]string{}
	nodes := []string{}
	for _, s := range ix.Symbols {
		caller := s.FullName()
		for _, c := range ix.Calls[caller] {
			if c == caller {
				continue
			}
			if _, ok := resolveName(ix, c); !ok {
				continue
			}
			if !containsStr(adj[caller], c) {
				adj[caller] = append(adj[caller], c)
			}
			if !containsStr(adj[c], caller) {
				adj[c] = append(adj[c], caller)
			}
			nodes = append(nodes, caller, c)
		}
	}
	nodes = dedupeSorted(nodes)
	if len(nodes) == 0 {
		return map[string]string{}
	}

	label := map[string]string{}
	for _, n := range nodes {
		label[n] = n
	}

	for iter := 0; iter < maxCommunityIterations; iter++ {
		changed := false
		for _, n := range nodes {
			counts := map[string]int{}
			for _, nb := range adj[n] {
				counts[label[nb]]++
			}
			if len(counts) == 0 {
				continue
			}
			bestCount := -1
			var candidates []string
			for l, c := range counts {
				if c > bestCount {
					bestCount = c
					candidates = []string{l}
				} else if c == bestCount {
					candidates = append(candidates, l)
				}
			}
			sort.Strings(candidates)
			best := candidates[0]
			if best != label[n] {
				label[n] = best
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return label
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
