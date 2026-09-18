// Graph mode for kern pack: instead of shipping file contents, pack the
// relevant call-graph snapshot — adjacency, one-line per-node signatures, and
// the per-file SHA-256 fingerprint — at roughly 1-5% of the raw file token
// cost for a subgraph. The receiver verifies freshness and hydrates source
// lazily via kern_context, only for the symbols it actually touches.

package pack

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// GraphBundle is the graph-mode pack result. It embeds the GraphSnapshot
// (schema version, mode, root, identity, graph, per-file fingerprint) so the
// bundle IS the snapshot format, and adds the flat sorted node list plus the
// one-line signatures rendered for handoff/review. Nothing is duplicated: the
// snapshot's Graph/Files/Identity travel untouched inside the bundle.
type GraphBundle struct {
	index.GraphSnapshot // embedded: SchemaVersion, Mode, Root, Identity, Graph, Files
	// Symbol is the subgraph root symbol ("" = whole graph).
	Symbol string `json:"symbol,omitempty"`
	// Nodes holds the packed node fullnames, sorted. It is a subset of
	// Graph.Nodes when maxTokens truncated the signature section.
	Nodes []string `json:"nodes"`
	// Signatures maps node fullname -> one-line signature.
	Signatures map[string]string `json:"signatures"`
	// TotalTokens is the token count of the packed signature section
	// (the dominant payload; header/edges/fingerprint add a small overhead).
	TotalTokens int `json:"total_tokens"`
}

// BuildGraph renders the index at root as a graph pack. An empty
// opts.GraphSymbol packs the whole graph (the snapshot's 400-symbol default
// cap); a non-empty symbol packs that symbol's neighbourhood and errors loudly
// when the symbol is unknown (mirroring Snapshot). opts.MaxTokens caps the
// signature section deterministically: lines are kept in sorted node order
// while the cumulative token count fits, then a "...N more" marker is
// appended, consistent with pack's skip-and-continue truncation style.
func BuildGraph(root string, opts Options) (*GraphBundle, error) {
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		return nil, fmt.Errorf("graph pack: load index: %w", err)
	}
	mode, symbol := "whole", ""
	if opts.GraphSymbol != "" {
		mode, symbol = "subgraph", opts.GraphSymbol
	}
	snap, err := ix.Snapshot(mode, symbol, 0)
	if err != nil {
		return nil, fmt.Errorf("graph pack: %w", err)
	}
	byID := make(map[string]index.GraphNode, len(snap.Graph.Nodes))
	for _, n := range snap.Graph.Nodes {
		byID[n.ID] = n
	}
	nodes := make([]string, 0, len(byID))
	for id := range byID {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)

	var packed []string
	var lines []string
	used := 0
	for _, id := range nodes {
		line := signatureLine(ix, byID[id])
		if opts.MaxTokens > 0 && used+tokenize.Count(line) > opts.MaxTokens {
			break
		}
		used += tokenize.Count(line)
		packed = append(packed, id)
		lines = append(lines, line)
	}
	sigs := make(map[string]string, len(packed))
	for i, id := range packed {
		sigs[id] = lines[i]
	}
	total := 0
	for _, l := range lines {
		total += tokenize.Count(l)
	}
	// The truncation marker is derived at Render time from len(Nodes) vs
	// len(Graph.Nodes); count it in TotalTokens so the reported number stays
	// honest when the cap cut in.
	if len(packed) < len(nodes) {
		total += tokenize.Count(fmt.Sprintf("... %d more symbols not packed (raise max_tokens)", len(nodes)-len(packed)))
	}
	return &GraphBundle{
		GraphSnapshot: snap,
		Symbol:        opts.GraphSymbol,
		Nodes:         packed,
		Signatures:    sigs,
		TotalTokens:   total,
	}, nil
}

// signatureLine renders a one-line signature for a graph node. When the node
// resolves to a recorded index.Symbol, the params/returns signature is used
// (the closest thing to a recorded signature index.Symbol carries); otherwise
// the "kind name — file:line" fallback.
func signatureLine(ix *index.Index, n index.GraphNode) string {
	if sym, ok := ix.FindSymbol(n.ID); ok {
		var b strings.Builder
		b.WriteString(sym.Kind)
		b.WriteString(" ")
		b.WriteString(sym.FullName())
		b.WriteString("(")
		b.WriteString(strings.Join(sym.Params, ", "))
		b.WriteString(")")
		if len(sym.Returns) > 0 {
			b.WriteString(" ")
			b.WriteString(strings.Join(sym.Returns, ", "))
		}
		b.WriteString(" — ")
		b.WriteString(sym.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(sym.Line))
		return b.String()
	}
	return fmt.Sprintf("%s %s — %s:%d", n.Kind, n.ID, n.File, n.Line)
}

// Render returns the paste-ready graph pack: header (root, symbol|whole,
// schema version, identity), one-line signatures, sorted edges with
// confidence, the per-file SHA-256 fingerprint, and a footer telling the
// receiver how to verify freshness and hydrate source per symbol. Output is
// deterministic for the same snapshot: nodes, edges, and files are all
// emitted in sorted order (GeneratedAt is deliberately not rendered).
func (b *GraphBundle) Render() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Graph-snapshot pack of %s (kern pack --graph).\n", b.Root)
	fmt.Fprintf(&out, "Packs the call graph instead of file contents: adjacency, one-line signatures, and a per-file SHA-256 fingerprint. Hydrate source per symbol lazily with kern_context.\n")
	fmt.Fprintf(&out, "schema: %d  mode: %s", b.SchemaVersion, b.Mode)
	if b.Symbol != "" {
		fmt.Fprintf(&out, "  symbol: %s", b.Symbol)
	} else {
		fmt.Fprintf(&out, "  symbol: whole graph")
	}
	fmt.Fprintf(&out, "\n")
	if oid := b.Identity.TreeOID; oid != "" {
		fmt.Fprintf(&out, "identity: tree %s\n", oid)
	} else if contentRoot := b.Identity.ContentRoot; contentRoot != "" {
		fmt.Fprintf(&out, "identity: %s\n", contentRoot)
	}
	out.WriteString("\n")

	fmt.Fprintf(&out, "== symbols (%d) ==\n", len(b.Signatures))
	for _, id := range b.Nodes {
		fmt.Fprintf(&out, "%s\n", b.Signatures[id])
	}
	if len(b.Nodes) < len(b.Graph.Nodes) {
		fmt.Fprintf(&out, "... %d more symbols not packed (raise max_tokens)\n", len(b.Graph.Nodes)-len(b.Nodes))
	}

	edges := make([]index.GraphEdge, len(b.Graph.Edges))
	copy(edges, b.Graph.Edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].ConfidenceLabel < edges[j].ConfidenceLabel
	})
	fmt.Fprintf(&out, "\n== edges (%d) ==\n", len(edges))
	for _, e := range edges {
		conf := e.ConfidenceLabel
		if conf == "" {
			conf = e.Confidence
		}
		fmt.Fprintf(&out, "%s -> %s [%s]\n", e.From, e.To, conf)
	}

	paths := make([]string, 0, len(b.Files))
	for p := range b.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fmt.Fprintf(&out, "\n== fingerprint (%d files) ==\n", len(paths))
	for _, p := range paths {
		fmt.Fprintf(&out, "%s  %s\n", b.Files[p], p)
	}

	fmt.Fprintf(&out, "\nVerify freshness with `kern snapshot verify <file> <root>` or re-hash these files with sha256sum; hydrate source for any symbol with `kern context <symbol>`.\n")
	return out.String()
}
