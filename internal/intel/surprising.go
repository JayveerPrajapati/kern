package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// maxCommunityDistance caps the alternate-path distance between two
// communities. A pair of communities that only connect through the
// cross-community edge itself (no alternate route in the community graph)
// gets this sentinel value: that coupling is the most surprising possible,
// so it must outrank any pair that has a real alternate route.
const maxCommunityDistance = 9

// SurprisingEdge is one cross-community project-local call edge (a caller in
// community A calls a symbol in community B, A != B) whose callee is not an
// already-known bridge. Ranking key: community distance x rarity, where
// distance is the shortest alternate path between the two communities in the
// community-contracted graph (their own direct edge excluded) and rarity is
// the inverse of the number of raw edges running between the pair.
type SurprisingEdge struct {
	Caller          string  `json:"caller"`
	Callee          string  `json:"callee"`
	File            string  `json:"file"` // callee definition site (evidence anchor)
	Line            int     `json:"line"`
	CallerCommunity string  `json:"caller_community"`
	CalleeCommunity string  `json:"callee_community"`
	Distance        int     `json:"distance"` // alternate-path hops; maxCommunityDistance when disconnected
	Edges           int     `json:"edges"`    // raw cross-community call edges between the pair
	Score           float64 `json:"score"`    // distance / edges — the rank key
}

// SurprisingConnections ranks cross-community call edges by community
// distance x rarity, deduped against known bridges (callees called from >= 2
// packages are the expected connectors, not surprises). Deterministic:
// symbols iterate in index order and the result is sorted by score desc,
// then distance desc, then caller, then callee. Returns nil when the index
// has no communities (no local call edges, or a repo over the community
// size cap) or when every cross-community call targets a bridge.
func SurprisingConnections(ix *index.Index, limit int) []SurprisingEdge {
	return rankSurprisingConnections(ix, ix.CommunityLabels(), limit)
}

// rankSurprisingConnections is the ranking core with an explicit community
// label map, so the distance/rarity/dedupe logic is testable without
// depending on label propagation's outcome. SurprisingConnections feeds it
// the index's own CommunityLabels; callers with a different clustering
// (e.g. a user-supplied community definition) can reuse it directly.
func rankSurprisingConnections(ix *index.Index, labels map[string]string, limit int) []SurprisingEdge {
	if limit <= 0 {
		limit = 15
	}
	if len(labels) == 0 {
		return nil
	}
	// Known connectors: symbols called from >= 2 distinct packages.
	bridges := map[string]bool{}
	for _, b := range Bridges(ix, 1<<30) {
		bridges[b.Symbol] = true
	}
	calleeSym := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		calleeSym[s.FullName()] = s
	}
	// resolveName maps a raw call target to its canonical label-key name,
	// mirroring resolveEdgeID (internal/context/rules.go): exact FullName
	// first, then the last path/name segment ("m.M1" -> "M1",
	// "intel.ReadIndex" -> "ReadIndex"). Raw call edges reference callees by
	// package- or receiver-qualified names while community labels are keyed
	// by bare FullName, so without this every cross-package edge would be
	// dropped. The caller then looks the NAME up in the labels map — the
	// resolved value must never be a label itself.
	resolveName := func(target string) (string, bool) {
		if _, ok := labels[target]; ok {
			return target, true
		}
		if i := strings.LastIndexAny(target, "./"); i >= 0 {
			if _, ok := labels[target[i+1:]]; ok {
				return target[i+1:], true
			}
		}
		return "", false
	}
	type pair struct{ a, b string }
	counts := map[pair]int{}
	samples := map[pair][]SurprisingEdge{}
	for _, s := range ix.Symbols {
		caller := s.FullName()
		ca, ok := labels[caller]
		if !ok || isTestFile(s.File) {
			continue
		}
		for _, ce := range ix.Calls[caller] {
			t := ce.Target
			resolved, ok := resolveName(t)
			cb, hasLabel := labels[resolved]
			if !ok || !hasLabel || cb == ca || bridges[resolved] {
				continue
			}
			key := pair{ca, cb}
			if cb < ca {
				key = pair{cb, ca}
			}
			counts[key]++
			if len(samples[key]) < 2 {
				sym, ok := calleeSym[resolved]
				if !ok {
					continue
				}
				samples[key] = append(samples[key], SurprisingEdge{
					Caller: caller, Callee: resolved,
					File: sym.File, Line: sym.Line,
					CallerCommunity: ca, CalleeCommunity: cb,
				})
			}
		}
	}
	if len(counts) == 0 {
		return nil
	}
	// Community-contracted graph: nodes are communities, edges are the
	// cross-community pairs. The distance between a pair excludes their own
	// direct edge, so it measures how far apart the two communities sit in
	// the rest of the topology.
	commAdj := map[string]map[string]bool{}
	for k := range counts {
		if commAdj[k.a] == nil {
			commAdj[k.a] = map[string]bool{}
		}
		if commAdj[k.b] == nil {
			commAdj[k.b] = map[string]bool{}
		}
		commAdj[k.a][k.b] = true
		commAdj[k.b][k.a] = true
	}
	altDist := func(a, b string) int {
		if a == b {
			return 0
		}
		visited := map[string]bool{a: true}
		queue := []string{a}
		for hops := 1; hops <= maxCommunityDistance; hops++ {
			var next []string
			for _, n := range queue {
				for nb := range commAdj[n] {
					if nb == b {
						// First hop only: the direct pair edge is the edge
						// being ranked, never the alternate route.
						if hops == 1 {
							continue
						}
						return hops
					}
					if !visited[nb] {
						visited[nb] = true
						next = append(next, nb)
					}
				}
			}
			if len(next) == 0 {
				break
			}
			queue = next
		}
		return maxCommunityDistance
	}
	out := []SurprisingEdge{}
	for k, n := range counts {
		for i := range samples[k] {
			e := samples[k][i]
			e.Distance = altDist(e.CallerCommunity, e.CalleeCommunity)
			e.Edges = n
			e.Score = float64(e.Distance) / float64(n)
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Distance != out[j].Distance {
			return out[i].Distance > out[j].Distance
		}
		if out[i].Caller != out[j].Caller {
			return out[i].Caller < out[j].Caller
		}
		return out[i].Callee < out[j].Callee
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RenderSurprising returns a compact human-readable report of the top
// surprising cross-community connections.
func RenderSurprising(edges []SurprisingEdge) string {
	if len(edges) == 0 {
		return "no surprising cross-community connections (all cross-community calls are known bridges)"
	}
	var b strings.Builder
	b.WriteString("surprising connections (cross-community calls, ranked by distance × rarity):\n")
	for _, e := range edges {
		distNote := fmt.Sprintf("dist %d", e.Distance)
		if e.Distance >= maxCommunityDistance {
			distNote = "disconnected communities (this edge is their only coupling)"
		}
		fmt.Fprintf(&b, "  %-34s %s:%d\n", e.Callee, e.File, e.Line)
		fmt.Fprintf(&b, "    called from %s · %s · %d edge(s) between communities\n",
			e.Caller, distNote, e.Edges)
	}
	return b.String()
}