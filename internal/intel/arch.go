package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ArchCommunity is one subsystem in the architecture view.
type ArchCommunity struct {
	ID       string   `json:"id"`
	Size     int      `json:"size"`
	Hub      string   `json:"hub,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

// CouplingEdge is a bundle of call edges flowing between two communities.
// High coupling between subsystems is an architecture risk: changes in one
// ripple into the other.
type CouplingEdge struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Count int      `json:"count"`
	Nodes []string `json:"nodes,omitempty"` // representative symbol pairs
}

// Architecture is the community map plus the inter-community coupling that a
// reviewer needs to judge architecture health.
type Architecture struct {
	Communities []ArchCommunity `json:"communities"`
	Coupling    []CouplingEdge  `json:"coupling"`
	// Gated is true when community detection was skipped to avoid O(n*iter)
	// latency on very large graphs.
	Gated       bool `json:"gated"`
	SymbolCount int  `json:"symbol_count"`
	EdgeCount   int  `json:"edge_count"`
}

// AnalyzeArchitecture computes the community structure and the cross-community
// call bundles. Every local call edge whose endpoints land in different
// communities contributes to one coupling bundle. For repos exceeding
// index.MaxCommunitySymbols, community detection is skipped (Gated=true).
func AnalyzeArchitecture(ix *index.Index) Architecture {
	if len(ix.Symbols) > index.MaxCommunitySymbols {
		edges := 0
		for _, callees := range ix.Calls {
			edges += len(callees)
		}
		return Architecture{
			Gated:       true,
			SymbolCount: len(ix.Symbols),
			EdgeCount:   edges,
		}
	}
	labels, nodes := labelPropagation(ix)
	fileMap := buildFileMap(ix)

	groups := map[string][]string{}
	for _, n := range nodes {
		groups[labels[n]] = append(groups[labels[n]], n)
	}

	var comms []ArchCommunity
	for id, syms := range groups {
		sort.Strings(syms)
		if len(syms) < minCommunitySize {
			continue
		}
		hub := ""
		best := -1
		packages := map[string]bool{}
		for _, s := range syms {
			if d := dirOf(fileMap, s); d != "" {
				packages[d] = true
			}
			if n := len(prodCallers(ix, s)); n > best {
				best = n
				hub = s
			}
		}
		var pkgs []string
		for p := range packages {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		comms = append(comms, ArchCommunity{ID: id, Size: len(syms), Hub: hub, Packages: pkgs})
	}
	sort.Slice(comms, func(i, j int) bool {
		if comms[i].Size != comms[j].Size {
			return comms[i].Size > comms[j].Size
		}
		return comms[i].ID < comms[j].ID
	})

	bundles := map[string]*CouplingEdge{}
	local := localNames(ix)
	for _, s := range ix.Symbols {
		caller := s.FullName()
		cl := labels[caller]
		if cl == "" {
			continue
		}
		for _, c := range localCalleesWith(ix, caller, local) {
			lc := labels[c]
			if lc == "" || lc == cl {
				continue
			}
			a, b := cl, lc
			if a > b {
				a, b = b, a
			}
			key := a + "\x00" + b
			e, ok := bundles[key]
			if !ok {
				e = &CouplingEdge{From: a, To: b}
				bundles[key] = e
			}
			e.Count++
			if len(e.Nodes) < 3 {
				e.Nodes = append(e.Nodes, caller+" -> "+c)
			}
		}
	}
	var coupling []CouplingEdge
	for _, e := range bundles {
		coupling = append(coupling, *e)
	}
	sort.Slice(coupling, func(i, j int) bool {
		if coupling[i].Count != coupling[j].Count {
			return coupling[i].Count > coupling[j].Count
		}
		return coupling[i].From+coupling[i].To < coupling[j].From+coupling[j].To
	})

	return Architecture{Communities: comms, Coupling: coupling}
}

// RenderArch returns the architecture report: subsystems first, then coupling
// warnings ranked by bundle size. When community detection was skipped, a
// skip note is rendered instead of a "no call structure" message.
func RenderArch(a Architecture) string {
	var b strings.Builder
	b.WriteString("architecture overview (communities + coupling):\n\n")
	if a.Gated {
		fmt.Fprintf(&b, "  (community detection skipped — %d symbols / %d call edges exceed the %d-symbol gate;\n"+
			"   use `kern hubs` and `kern bridges` for structural analysis on gated repos)\n",
			a.SymbolCount, a.EdgeCount, index.MaxCommunitySymbols)
		return b.String()
	}
	if len(a.Communities) == 0 {
		b.WriteString("  (no project-local call structure detected)\n")
	}
	for _, c := range a.Communities {
		fmt.Fprintf(&b, "  %-24s size %-4d hub %-24s pkgs %s\n",
			c.ID, c.Size, c.Hub, strings.Join(c.Packages, ", "))
	}
	b.WriteString("\ncoupling warnings (cross-community call bundles):\n")
	if len(a.Coupling) == 0 {
		b.WriteString("  (communities are cleanly separated)\n")
	}
	shown := a.Coupling
	if len(shown) > 12 {
		shown = shown[:12]
	}
	for _, e := range shown {
		fmt.Fprintf(&b, "  %-24s <-> %-24s %4d edges\n", e.From, e.To, e.Count)
		for _, n := range e.Nodes {
			fmt.Fprintf(&b, "      %s\n", n)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
