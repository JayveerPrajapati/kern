package intel

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Hub is a symbol ranked by how much of the codebase depends on it.
// Score is the raw dependency weight (callers*2 + calls); Weighted is the
// community-normalized score used for ranking — a hub inside a huge community
// is less surprising (less hub-like) than one in a tiny community. Weighted
// equals Score when the index carries no community labels or the symbol has
// none. Score stays the raw int for backward-compatible JSON output.
type Hub struct {
	Symbol   string  `json:"symbol"`
	Kind     string  `json:"kind"`
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Callers  int     `json:"callers"`
	Calls    int     `json:"calls"`
	Score    int     `json:"score"`
	Weighted float64 `json:"weighted,omitempty"`
	// bare is the unqualified FullName (the index Callers/Calls key). It is
	// unexported so JSON output is unchanged; hubSet uses it to keep bare
	// lookups working for risk scoring (P1-5).
	bare string
}

// Bridge is a symbol whose callers span multiple packages — a coupling
// chokepoint where a change in one subsystem can break another.
type Bridge struct {
	Symbol   string   `json:"symbol"`
	File     string   `json:"file"`
	Callers  int      `json:"callers"`
	Packages []string `json:"packages"`
}

// hubSet returns the top-decile symbols by community-weighted dependency
// score; used to weight risk scores for changes touching architectural
// hotspots. It shares Hubs' normalized ranking (same limit semantics), so the
// wiki hub flags stay consistent with kern hubs output.
func hubSet(ix *index.Index) map[string]bool {
	ranked := Hubs(ix, 0)
	if len(ranked) == 0 {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, h := range ranked[:max(len(ranked)/10, min(len(ranked), 5))] {
		out[h.Symbol] = true
		// P1-5: hub symbols for ambiguous names are package-qualified
		// (internal/cache.Load), but risk scoring looks hubs up by bare
		// FullName and call-edge target. Keep the bare key so existing
		// lookups keep working (conservative: any same-named hub flags).
		if h.bare != "" && h.bare != h.Symbol {
			out[h.bare] = true
		}
	}
	return out
}

// Hubs returns symbols ranked by combined dependency weight (callers + calls),
// normalized by community size: a hub in a huge community is less surprising
// than one in a small community, so its score is divided by the square root of
// its community's symbol count. Indexes without community labels (or symbols
// with none) keep their raw score — ranking then falls back to raw weight.
// limit<=0 means "top 10%".
func Hubs(ix *index.Index, limit int) []Hub {
	var hubs []Hub
	local := localNames(ix)
	fileMap := buildFileMap(ix)
	pkgByFile := packagePathByFile(ix)
	dups := dupFullNames(ix)
	units := defUnits(ix, pkgByFile, dups)
	byName := unitsByName(units)
	// Caller splits are computed once per ambiguous name, not once per unit.
	splits := map[string]map[string][]string{}
	commSize := communitySizes(ix)
	for _, u := range units {
		if isTestFile(u.file) || (u.sym.Kind != "func" && u.sym.Kind != "method") {
			continue
		}
		callers := hubUnitCallers(ix, fileMap, dups, byName, splits, u)
		// NOTE (P1-5): outgoing calls stay aggregated by bare name —
		// ix.Calls merges every same-named definition's callees with no
		// per-file provenance, so per-definition calls cannot be recovered
		// at this layer. Caller counts (the bridge/hub signal that matters)
		// are honestly split; calls may over-count for ambiguous names.
		calls := len(localCalleesWith(ix, u.name, local))
		if len(callers) == 0 && calls == 0 {
			continue
		}
		raw := len(callers)*2 + calls
		hubs = append(hubs, Hub{
			Symbol: u.qual, Kind: u.sym.Kind, File: u.file, Line: u.sym.Line,
			Callers: len(callers), Calls: calls, Score: raw,
			Weighted: weightedHubScore(raw, u.name, ix.Communities, commSize),
			bare:     u.name,
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

// communitySizes counts the indexed symbols belonging to each community label,
// keyed by label. It counts every symbol in ix.Symbols (not just hub
// candidates), so the normalization denominator matches the community's true
// footprint. Returns nil when the index carries no community labels, which
// disables hub normalization entirely.
func communitySizes(ix *index.Index) map[string]int {
	if len(ix.Communities) == 0 {
		return nil
	}
	sizes := make(map[string]int, len(ix.Communities))
	for _, s := range ix.Symbols {
		if label, ok := ix.Communities[s.FullName()]; ok {
			sizes[label]++
		}
	}
	return sizes
}

// weightedHubScore normalizes a hub's raw weight by its community size:
// score = raw / sqrt(community size). The square root softens the penalty
// versus a hard division — a hub in a 2-symbol community keeps ~70% of its
// raw weight, while one in a 100-symbol community drops to 10%. Symbols
// without a community label (or indexes with no Communities) keep their raw
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
	fileMap := buildFileMap(ix)
	pkgByFile := packagePathByFile(ix)
	dups := dupFullNames(ix)
	units := defUnits(ix, pkgByFile, dups)
	byName := unitsByName(units)
	splits := map[string]map[string][]string{}
	var bridges []Bridge
	for _, u := range units {
		if isTestFile(u.file) || (u.sym.Kind != "func" && u.sym.Kind != "method") {
			continue
		}
		callers := hubUnitCallers(ix, fileMap, dups, byName, splits, u)
		dirs := map[string]bool{}
		for _, c := range callers {
			if d := dirOf(fileMap, c); d != "" {
				dirs[d] = true
			}
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
			Symbol: u.qual, File: u.file, Callers: len(callers), Packages: pkgs,
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

// Render returns a compact human-readable hubs report.
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
