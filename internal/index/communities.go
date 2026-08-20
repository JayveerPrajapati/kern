package index

import "sort"

const (
	maxCommunityIterations = 30
	minCommunitySize       = 2
	MaxCommunitySymbols    = 50000
)

// CommunityLabels clusters symbols with deterministic label propagation over
// project-local call edges (external callees ignored). Keys are symbol full
// names; values are community labels. Returns empty for repos over
// MaxCommunitySymbols to avoid O(n*iter) latency.
func (ix *Index) CommunityLabels() map[string]string {
	if len(ix.Symbols) > MaxCommunitySymbols {
		return map[string]string{}
	}
	// Build a set of project-local symbol full names so we can skip external
	// callees (JDK types like List.of, Date, Optional.of) without relying on
	// resolveName's bare-name fallback, which would let an external call
	// "java.util.List.of" resolve to a project method named "of".
	localFull := map[string]bool{}
	for _, s := range ix.Symbols {
		localFull[s.FullName()] = true
	}
	adj := map[string][]string{}
	nodes := []string{}
	for _, s := range ix.Symbols {
		caller := s.FullName()
		for _, c := range ix.Calls[caller] {
			if c == caller {
				continue
			}
			// Only include callees that are themselves project-local symbols.
			// This excludes external/stdlib call targets that would otherwise
			// dominate communities (e.g. "Date", "List.of", "Optional.of").
			if !localFull[c] {
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
