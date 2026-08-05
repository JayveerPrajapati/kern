package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Hub is a symbol ranked by how much of the codebase depends on it.
type Hub struct {
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Callers int    `json:"callers"`
	Calls   int    `json:"calls"`
	Score   int    `json:"score"`
}

// Bridge is a symbol whose callers span multiple packages — a coupling
// chokepoint where a change in one subsystem can break another.
type Bridge struct {
	Symbol   string   `json:"symbol"`
	File     string   `json:"file"`
	Callers  int      `json:"callers"`
	Packages []string `json:"packages"`
}

// hubSet returns the top-decile symbols by incoming caller count; used to
// weight risk scores for changes touching architectural hotspots.
func hubSet(ix *index.Index) map[string]bool {
	ranked := Hubs(ix, 0)
	if len(ranked) == 0 {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, h := range ranked[:max(len(ranked)/10, min(len(ranked), 5))] {
		out[h.Symbol] = true
	}
	return out
}

// Hubs returns symbols ranked by combined dependency weight (callers + calls).
// limit<=0 means "top 10%".
func Hubs(ix *index.Index, limit int) []Hub {
	var hubs []Hub
	for _, s := range ix.Symbols {
		if isTestFile(s.File) || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		callers := len(prodCallers(ix, s.FullName()))
		calls := len(localCallees(ix, s.FullName()))
		if callers == 0 && calls == 0 {
			continue
		}
		hubs = append(hubs, Hub{
			Symbol: s.FullName(), Kind: s.Kind, File: s.File, Line: s.Line,
			Callers: callers, Calls: calls, Score: callers*2 + calls,
		})
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].Callers != hubs[j].Callers {
			return hubs[i].Callers > hubs[j].Callers
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

// Bridges returns symbols called from more than one package, ranked by the
// number of packages they couple.
func Bridges(ix *index.Index, limit int) []Bridge {
	if limit <= 0 {
		limit = 15
	}
	fileMap := buildFileMap(ix)
	var bridges []Bridge
	for _, s := range ix.Symbols {
		if isTestFile(s.File) || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		callers := prodCallers(ix, s.FullName())
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
			Symbol: s.FullName(), File: s.File, Callers: len(callers), Packages: pkgs,
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
		fmt.Fprintf(&b, "  %-6s %-32s %s:%d  (%d callers, %d calls, score %d)\n",
			h.Kind, h.Symbol, h.File, h.Line, h.Callers, h.Calls, h.Score)
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
