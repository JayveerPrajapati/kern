package intel

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Community is a cluster of symbols discovered by label propagation over the
// (undirected) call graph.
// By default the JSON output omits the full symbol list (Symbols) and exposes
// only a Sample (first few names) plus Size — enough to name a cluster without
// dumping every member. Call WithFullSymbols to render the complete list.
type Community struct {
	ID       string   `json:"id"`
	Symbols  []string `json:"-"`      // full member list; hidden in JSON unless FullSymbols is set
	Sample   []string `json:"sample"` // first up to 8 symbol names, for a quick glance
	Size     int      `json:"size"`
	Hub      string   `json:"hub,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

// FullSymbols returns a copy of the community's full symbol list. Used by the
// JSON marshaler path when the caller asks for the complete (verbose) output.
func (c Community) FullSymbols() []string {
	if len(c.Symbols) == 0 {
		return nil
	}
	out := make([]string, len(c.Symbols))
	copy(out, c.Symbols)
	return out
}

// communityJSON is the on-wire shape used by MarshalCommunities. When Full is
// false, Symbols is omitted; when true, the complete member list is included.
type communityJSON struct {
	ID       string   `json:"id"`
	Symbols  []string `json:"symbols,omitempty"`
	Sample   []string `json:"sample"`
	Size     int      `json:"size"`
	Hub      string   `json:"hub,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

// MarshalCommunities renders communities as JSON. When full is false (the
// default) each community carries only a Sample of its members; when true the
// complete Symbols list is emitted (the legacy verbose behaviour).
func MarshalCommunities(comms []Community, full bool) []byte {
	out := make([]communityJSON, len(comms))
	for i, c := range comms {
		cj := communityJSON{
			ID:       c.ID,
			Sample:   c.Sample,
			Size:     c.Size,
			Hub:      c.Hub,
			Packages: c.Packages,
		}
		if full {
			cj.Symbols = c.FullSymbols()
		}
		out[i] = cj
	}
	b, _ := json.Marshal(out)
	return b
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
// participates in at least one local call edge, plus the sorted node list.
// The clustering itself lives in the index package (Index.CommunityLabels).
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
		// Choose the community ID: prefer the first project-local symbol
		// over external/stdlib names that would dominate the community name.
		communityID := syms[0]
		for _, s := range syms {
			if fileMap[s] != "" {
				communityID = s
				break
			}
		}
		hub := ""
		best := -1
		packages := map[string]bool{}
		for _, s := range syms {
			if d := dirOf(fileMap, s); d != "" {
				packages[d] = true
			}
			// Only consider project-local symbols for the hub role.
			if fileMap[s] == "" {
				continue
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
		// Sample: first up to 8 sorted member names, enough to identify the
		// cluster without dumping every symbol in JSON output.
		sample := syms
		if len(sample) > 8 {
			sample = sample[:8]
		}
		out = append(out, Community{
			ID: communityID, Symbols: syms, Sample: sample, Size: len(syms), Hub: hub, Packages: pkgs,
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
