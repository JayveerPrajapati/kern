package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Community is a cluster of symbols discovered by label propagation over the
// (undirected) call graph — a dependency-free stand-in for Leiden/Newman
// modularity clustering.
type Community struct {
	ID       string   `json:"id"`
	Symbols  []string `json:"symbols"`
	Size     int      `json:"size"`
	Hub      string   `json:"hub,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

const (
	maxCommunityIterations = 30
	minCommunitySize       = 2
)

// Communities clusters symbols with label propagation over project-local call
// edges (stdlib/external callees are ignored). The result is deterministic:
// labels are initialised to the sorted symbol name and updated in a fixed
// order with lexicographic tie-breaking.
func Communities(ix *index.Index) []Community {
	labels, _ := labelPropagation(ix)
	return renderCommunities(ix, labels)
}

// labelPropagation returns the community label of every symbol that
// participates in at least one local call edge. The clustering itself lives
// in the index package (Index.CommunityLabels) so the SQLite store can
// persist it; this wrapper restores the sorted node list for rendering.
func labelPropagation(ix *index.Index) (map[string]string, []string) {
	label := ix.CommunityLabels()
	nodes := make([]string, 0, len(label))
	for n := range label {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return label, nodes
}

func renderCommunities(ix *index.Index, label map[string]string) []Community {
	groups := map[string][]string{}
	for n, l := range label {
		groups[l] = append(groups[l], n)
	}

	fileMap := buildFileMap(ix)
	var out []Community
	for _, syms := range groups {
		sort.Strings(syms)
		// Skip tiny self-clusters.
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
		out = append(out, Community{
			ID: syms[0], Symbols: syms, Size: len(syms), Hub: hub, Packages: pkgs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RenderCommunities returns a compact human-readable clustering report.
func RenderCommunities(comms []Community) string {
	var b strings.Builder
	b.WriteString("code communities (call-graph clusters):\n")
	for _, c := range comms {
		fmt.Fprintf(&b, "  %-28s size %-4d hub %-24s pkgs %s\n",
			c.ID, c.Size, c.Hub, strings.Join(c.Packages, ", "))
		shown := c.Symbols
		if len(shown) > 10 {
			shown = shown[:10]
			fmt.Fprintf(&b, "    %s … (+%d)\n", strings.Join(shown, ", "), len(c.Symbols)-10)
		} else {
			fmt.Fprintf(&b, "    %s\n", strings.Join(shown, ", "))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
