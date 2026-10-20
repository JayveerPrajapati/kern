package topology

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Hub is a symbol ranked by how much of the codebase depends on it.
// Score is the raw dependency weight (callers*2 + calls); Weighted is the
// community-normalized score used for ranking — a hub inside a huge community
// is less surprising (less hub-like) than one in a tiny community. Weighted
// equals Score when declaration clustering is unavailable or the symbol has
// none. Score stays the raw int for backward-compatible JSON output.
type Hub struct {
	ID       string  `json:"id"`
	Symbol   string  `json:"symbol"`
	Kind     string  `json:"kind"`
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Callers  int     `json:"callers"`
	Calls    int     `json:"calls"`
	Score    int     `json:"score"`
	Weighted float64 `json:"weighted,omitempty"`
}

// Bridge is a symbol whose callers span multiple packages — a coupling
// chokepoint where a change in one subsystem can break another.
type Bridge struct {
	ID       string   `json:"id"`
	Symbol   string   `json:"symbol"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Callers  int      `json:"callers"`
	Packages []string `json:"packages"`
}

// HubSet returns the top-decile symbols by community-weighted dependency
// score; used to weight risk scores for changes touching architectural
// hotspots. It shares Hubs' normalized ranking (same limit semantics), so the
// wiki hub flags stay consistent with kern hubs output.
func HubSet(ix *index.Index) map[string]bool {
	ranked := Hubs(ix, 0)
	if len(ranked) == 0 {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, h := range ranked[:max(len(ranked)/10, min(len(ranked), 5))] {
		out[h.ID] = true
	}
	return out
}

// Hubs returns symbols ranked by combined dependency weight (callers + calls),
// normalized by community size: a hub in a huge community is less surprising
// than one in a small community, so its score is divided by the square root of
// its community's declaration count. Symbols without a declaration community
// keep their raw score — ranking then falls back to raw weight.
// limit<=0 means "top 10%".
//
// Phase 5 (2026-10-07): test symbols and testdata fixtures are excluded by
// default — a defining file that is a test file (IsTestFile) or a testdata/
// fixture (IsFixtureFile) never ranks as a hub, so T.Fatalf-style test noise
// cannot dominate hub surfaces (buddy digest, kern hubs, web dashboards).
// No current caller needs the unfiltered set (fragility/hotspot analyses
// read ix.Callers directly), so there is no opt-in. An index whose symbols
// are ALL test/fixture files yields zero hubs (no unfiltered fallback): hub
// sections render empty, which is honest — there are no production hotspots —
// and keeps the exclusion absolute.
func Hubs(ix *index.Index, limit int) []Hub {
	return rankHubs(ix, ix.DeclarationCommunities(), limit)
}

func rankHubs(ix *index.Index, labels map[string]string, limit int) []Hub {
	var hubs []Hub
	names := ix.DeclarationNames()
	definitions, incoming, outgoing := declarationDependencies(ix)
	commSize := communitySizes(labels)
	for id, symbol := range definitions {
		if symbol.Kind != "func" && symbol.Kind != "method" {
			continue
		}
		callers, calls := len(incoming[id]), len(outgoing[id])
		if callers == 0 && calls == 0 {
			continue
		}
		raw := callers*2 + calls
		hubs = append(hubs, Hub{
			ID: id, Symbol: names[id], Kind: symbol.Kind, File: symbol.File, Line: symbol.Line,
			Callers: callers, Calls: calls, Score: raw,
			Weighted: weightedHubScore(raw, id, labels, commSize),
		})
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].Weighted != hubs[j].Weighted {
			return hubs[i].Weighted > hubs[j].Weighted
		}
		if hubs[i].Score != hubs[j].Score {
			return hubs[i].Score > hubs[j].Score
		}
		return hubs[i].Symbol < hubs[j].Symbol
	})
	if limit <= 0 {
		limit = max(len(hubs)/10, min(len(hubs), 8))
	}
	if len(hubs) > limit {
		hubs = hubs[:limit]
	}
	return hubs
}

// communitySizes counts declaration memberships per label, including non-hub
// declarations, so normalization reflects the community's full footprint.
func communitySizes(labels map[string]string) map[string]int {
	sizes := make(map[string]int)
	for _, label := range labels {
		sizes[label]++
	}
	return sizes
}

// weightedHubScore normalizes a hub's raw weight by its community size:
// score = raw / sqrt(community size). The square root softens the penalty
// versus a hard division — a hub in a 2-symbol community keeps ~70% of its
// raw weight, while one in a 100-symbol community drops to 10%. Symbols
// without a declaration-community label keep their raw
// score unchanged; a degenerate zero-size label also falls back to raw.
func weightedHubScore(raw int, sym string, communities map[string]string, commSize map[string]int) float64 {
	if len(commSize) == 0 {
		return float64(raw)
	}
	label, ok := communities[sym]
	if !ok {
		return float64(raw)
	}
	size := commSize[label]
	if size < 1 {
		return float64(raw)
	}
	return float64(raw) / math.Sqrt(float64(size))
}

// Bridges returns symbols called from more than one package, ranked by the
// number of packages they couple.
func Bridges(ix *index.Index, limit int) []Bridge {
	if limit <= 0 {
		limit = 15
	}
	names := ix.DeclarationNames()
	definitions, incoming, _ := declarationDependencies(ix)
	var bridges []Bridge
	for id, symbol := range definitions {
		if symbol.Kind != "func" && symbol.Kind != "method" {
			continue
		}
		callers := incoming[id]
		dirs := map[string]bool{}
		for caller := range callers {
			dirs[filepath.ToSlash(filepath.Dir(definitions[caller].File))] = true
		}
		if len(dirs) < 2 {
			continue
		}
		var pkgs []string
		for d := range dirs {
			pkgs = append(pkgs, d)
		}
		sort.Strings(pkgs)
		bridges = append(bridges, Bridge{
			ID: id, Symbol: names[id], File: symbol.File, Line: symbol.Line, Callers: len(callers), Packages: pkgs,
		})
	}
	sort.Slice(bridges, func(i, j int) bool {
		if len(bridges[i].Packages) != len(bridges[j].Packages) {
			return len(bridges[i].Packages) > len(bridges[j].Packages)
		}
		if bridges[i].Callers != bridges[j].Callers {
			return bridges[i].Callers > bridges[j].Callers
		}
		return bridges[i].Symbol < bridges[j].Symbol
	})
	if len(bridges) > limit {
		bridges = bridges[:limit]
	}
	return bridges
}

func declarationDependencies(ix *index.Index) (map[string]index.Symbol, map[string]map[string]bool, map[string]map[string]bool) {
	definitions := map[string]index.Symbol{}
	byName := map[string][]string{}
	for _, symbol := range ix.Symbols {
		if code.IsTestFile(symbol.File) || code.IsFixtureFile(symbol.File) {
			continue
		}
		id := symbol.DeclarationID()
		if _, exists := definitions[id]; !exists {
			definitions[id] = symbol
			byName[symbol.FullName()] = append(byName[symbol.FullName()], id)
		}
	}
	incoming, outgoing := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, edge := range ResolvedCommunityCalls(ix) {
		if _, found := definitions[edge.From]; !found {
			continue
		}
		if _, found := definitions[edge.To]; !found {
			continue
		}
		if incoming[edge.To] == nil {
			incoming[edge.To] = map[string]bool{}
		}
		if outgoing[edge.From] == nil {
			outgoing[edge.From] = map[string]bool{}
		}
		incoming[edge.To][edge.From] = true
		outgoing[edge.From][edge.To] = true
	}
	if len(ix.Calls) == 0 {
		for target, callers := range ix.Callers {
			if len(byName[target]) != 1 {
				continue
			}
			to := byName[target][0]
			for _, caller := range callers {
				if len(byName[caller]) != 1 || caller == target {
					continue
				}
				if incoming[to] == nil {
					incoming[to] = map[string]bool{}
				}
				incoming[to][byName[caller][0]] = true
			}
		}
	}
	return definitions, incoming, outgoing
}

// RenderHubs returns a compact human-readable hubs report.
func RenderHubs(hubs []Hub) string {
	var b strings.Builder
	b.WriteString("hub symbols (most depended-on):\n")
	for _, h := range hubs {
		fmt.Fprintf(&b, "  %-6s %-32s %s:%d  (%d callers, %d calls, score %d, weighted %.1f)\n",
			h.Kind, h.Symbol, h.File, h.Line, h.Callers, h.Calls, h.Score, h.Weighted)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// RenderBridges returns a compact human-readable bridges report.
func RenderBridges(bridges []Bridge) string {
	var b strings.Builder
	b.WriteString("bridge symbols (couple packages — change with care):\n")
	for _, br := range bridges {
		fmt.Fprintf(&b, "  %-32s %s  (%d callers across %s)\n",
			br.Symbol, br.File, br.Callers, strings.Join(br.Packages, ", "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}
