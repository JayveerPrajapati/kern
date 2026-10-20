package index

import (
	"path/filepath"
	"slices"
	"sort"
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/calleefilter"
)

func (ix *Index) DeclarationNames() map[string]string {
	return newExportCatalog(ix).names
}

func declarationNames(symbols map[string]Symbol) map[string]string {
	names := map[string]string{}
	counts := map[string]int{}
	for id, symbol := range symbols {
		names[id] = symbol.FullName()
		counts[names[id]]++
	}
	for id, symbol := range symbols {
		if counts[names[id]] > 1 {
			names[id] = filepath.ToSlash(filepath.Dir(symbol.File)) + "." + symbol.FullName()
		}
	}
	counts = map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	for id, symbol := range symbols {
		if counts[names[id]] > 1 {
			names[id] += " (" + filepath.ToSlash(symbol.File) + ":" + strconv.Itoa(symbol.Line) + ")"
		}
	}
	counts = map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	for id, name := range names {
		if counts[name] > 1 {
			names[id] = name + " [" + id + "]"
		}
	}
	return names
}

const (
	maxCommunityIterations = 30
	minCommunitySize       = 2
	MaxCommunitySymbols    = 50000
)

// CommunityLabels clusters project-local symbols via label propagation (test symbols excluded); empty over MaxCommunitySymbols.
func (ix *Index) CommunityLabels() map[string]string {
	if len(ix.Symbols) > MaxCommunitySymbols {
		return map[string]string{}
	}
	syms := slices.DeleteFunc(append([]Symbol(nil), ix.Symbols...), func(s Symbol) bool { return calleefilter.IsTestSymbol(s.File) })
	localFull := map[string]bool{}
	for _, s := range syms {
		localFull[s.FullName()] = true
	}
	adj := map[string][]string{}
	adjSet := map[string]map[string]bool{}
	nodes := []string{}
	for _, s := range syms {
		caller := s.FullName()
		for _, ce := range ix.Calls[caller] {
			c := ce.Target
			if c == caller {
				continue
			}
			// Only include callees that are themselves project-local symbols.
			// This excludes external/stdlib call targets that would otherwise
			// dominate communities (e.g. "Date", "List.of", "Optional.of").
			if !localFull[c] {
				continue
			}
			// Set-membership dedup keeps adjacency insertion O(1) per edge
			// instead of the old containsStr linear scan (O(degree) per
			// insert, quadratic on hub files with thousands of calls).
			if !adjSet[caller][c] {
				if adjSet[caller] == nil {
					adjSet[caller] = map[string]bool{}
				}
				adjSet[caller][c] = true
				adj[caller] = append(adj[caller], c)
			}
			if !adjSet[c][caller] {
				if adjSet[c] == nil {
					adjSet[c] = map[string]bool{}
				}
				adjSet[c][caller] = true
				adj[c] = append(adj[c], caller)
			}
			nodes = append(nodes, caller, c)
		}
	}
	return propagateLabels(adj, dedupeSorted(nodes))
}

// DeclarationCommunities clusters declarations by declaration ID using only
// resolved, source-attributed call bindings, so same-named declarations never
// share a membership. Persisted name-keyed labels (ix.Communities) remain a
// display aggregate; this map is the identity-correct view.
func (ix *Index) DeclarationCommunities() map[string]string {
	cache := ix.getCache()
	cache.communities.Do(func() {
		cache.declComm = ix.declarationCommunities()
	})
	return cache.declComm
}

func (ix *Index) declarationCommunities() map[string]string {
	if len(ix.Symbols) > MaxCommunitySymbols {
		return map[string]string{}
	}
	catalog := newExportCatalog(ix)
	local := map[string]bool{}
	for id, symbol := range catalog.byID {
		if !calleefilter.IsTestSymbol(symbol.File) {
			local[id] = true
		}
	}
	adj := map[string][]string{}
	seen := map[[2]string]bool{}
	var nodes []string
	for _, call := range catalog.calls(nil) {
		from, to := call.from.ID, call.to.ID
		if call.edge.Resolution != "resolved" || from == to || !local[from] || !local[to] || seen[[2]string{from, to}] {
			continue
		}
		seen[[2]string{from, to}] = true
		adj[from] = append(adj[from], to)
		if !seen[[2]string{to, from}] {
			seen[[2]string{to, from}] = true
			adj[to] = append(adj[to], from)
		}
		nodes = append(nodes, from, to)
	}
	nodes = dedupeSorted(nodes)
	labels := propagateLabels(adj, nodes)
	display := map[string]string{}
	for id, representative := range labels {
		display[id] = catalog.names[representative]
	}
	return display
}

// CommunityMembers returns the sorted member names of the community containing
// the declaration, or nil when it has no local resolved call edges or its
// community has fewer than minCommunitySize members.
func (ix *Index) CommunityMembers(declarationID string) []string {
	labels := ix.DeclarationCommunities()
	label, ok := labels[declarationID]
	if !ok {
		return nil
	}
	catalog := newExportCatalog(ix)
	var members []string
	for id, other := range labels {
		if other == label {
			members = append(members, catalog.names[id])
		}
	}
	if len(members) < minCommunitySize {
		return nil
	}
	sort.Strings(members)
	return members
}

func propagateLabels(adj map[string][]string, nodes []string) map[string]string {
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

// containsStr reports whether s appears in list. Production adjacency
// building uses set-based dedup (see CommunityLabels); this helper remains
// for the package's tests.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
