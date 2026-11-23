// Package topology holds kern's graph-structure and community family: the
// architecture view (communities + coupling), hub/bridge hotspots, purity
// checking, and surprising cross-community connections. It was extracted from
// internal/intel; intel re-exports Violation as an alias and keeps the
// IsTestFile/IsFixtureFile predicates in internal/code.
package topology

import (
	"fmt"
	"path/filepath"
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
// call bundles. Every resolved declaration pair whose endpoints land in different
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
	labels, _ := labelPropagation(ix)
	names := ix.DeclarationNames()
	communityIDs := map[string]string{}
	for id, label := range labels {
		name := names[id]
		if current := communityIDs[label]; current == "" || name < current {
			communityIDs[label] = name
		}
	}
	var comms []ArchCommunity
	for _, community := range renderCommunities(ix, labels) {
		comms = append(comms, ArchCommunity{ID: community.ID, Size: community.Size, Hub: community.Hub, Packages: community.Packages})
	}

	bundles := map[string]*CouplingEdge{}
	for _, edge := range ResolvedCommunityCalls(ix) {
		cl, lc := labels[edge.From], labels[edge.To]
		if cl == "" || lc == "" || cl == lc {
			continue
		}
		a, b := communityIDs[cl], communityIDs[lc]
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
			e.Nodes = append(e.Nodes, names[edge.From]+" -> "+names[edge.To])
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

// communityLabel names a community by its dominant package families so the
// architecture view answers "what is this subsystem?" instead of showing an
// arbitrary member symbol. Falls back to the community id when no package
// metadata is available.
func communityLabel(c ArchCommunity) string {
	if len(c.Packages) == 0 {
		return c.ID
	}
	counts := make(map[string]int, len(c.Packages))
	for _, p := range c.Packages {
		counts[packageFamily(p)]++
	}
	type fam struct {
		name string
		n    int
	}
	fams := make([]fam, 0, len(counts))
	for k, n := range counts {
		fams = append(fams, fam{k, n})
	}
	sort.Slice(fams, func(i, j int) bool {
		if fams[i].n != fams[j].n {
			return fams[i].n > fams[j].n
		}
		return fams[i].name < fams[j].name
	})
	label := fams[0].name
	if len(fams) > 1 {
		label += "+" + fams[1].name
	}
	if len(fams) > 2 {
		label += "+…"
	}
	return label
}

// packageFamily collapses a package path to its family prefix: internal/mcp/graph
// and internal/mcp/exec both belong to internal/mcp (the extracted family the
// ledger organizes around); a top-level package is its own family.
func packageFamily(p string) string {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(p), "./"), "/")
	if len(parts) >= 3 && parts[0] == "internal" {
		return parts[0] + "/" + parts[1]
	}
	if len(parts) >= 2 && parts[0] == "cmd" {
		return parts[0] + "/" + parts[1]
	}
	return p
}

// wrapColumn renders pkgs joined with ", " so no line exceeds maxCol visible
// columns given the current line already holds prefix (prefix ends with the
// "pkgs " column). Continuation lines are prefixed with 6 spaces. Used by the
// arch/communities renderers to keep wide package lists readable.
func wrapColumn(pkgs []string, prefix string, maxCol int) string {
	var b strings.Builder
	col := len(prefix)
	for i, p := range pkgs {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		if i > 0 && col+len(sep)+len(p) > maxCol {
			b.WriteString("\n      ")
			col = 6
			sep = ""
		}
		b.WriteString(sep + p)
		col += len(sep) + len(p)
	}
	return b.String()
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
	// Communities used to be labelled by an arbitrary member symbol ("Anchor",
	// "Cache") — a label that told the reader nothing about what the
	// community IS. Label by dominant package family instead, keeping the
	// community id as the stable key for coupling rows (2026-11-10 campaign).
	labels := make(map[string]string, len(a.Communities))
	for _, c := range a.Communities {
		labels[c.ID] = communityLabel(c)
	}
	for _, c := range a.Communities {
		prefix := fmt.Sprintf("  %-24s size %-4d hub %-24s pkgs ", labels[c.ID], c.Size, c.Hub)
		fmt.Fprintf(&b, "%s%s\n", prefix, wrapColumn(c.Packages, prefix, 100))
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
		from, to := e.From, e.To
		if l, ok := labels[from]; ok {
			from = l
		}
		if l, ok := labels[to]; ok {
			to = l
		}
		fmt.Fprintf(&b, "  %-24s <-> %-24s %4d edges\n", from, to, e.Count)
		for _, n := range e.Nodes {
			fmt.Fprintf(&b, "      %s\n", n)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
