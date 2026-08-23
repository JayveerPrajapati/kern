// Package intelligence promotes the persisted v1 index into the canonical
// domain.Graph, adding provenance and version metadata, and exposes the unified
// query APIs (who calls X, what X depends on, what depends on X, and the
// affected APIs/services/events/tests). Graph wraps domain.Graph so the query
// methods hang off the canonical type while keeping full field access.
package intelligence

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Graph is the canonical knowledge graph. It embeds domain.Graph so all fields
// are promoted, while the query methods in queries.go extend the domain type.
// entries tracks the symbol node IDs that are framework entry points; this
// status lives here rather than on domain.Symbol, which deliberately has no
// Entry field.
type Graph struct {
	domain.Graph
	entries map[string]bool

	// Read-only-after-construction caches. The graph is built once (see
	// FromIndex / NewWithGraph) and treated as immutable afterwards, so these
	// are lazily initialized on first query and reused for the graph's whole
	// lifetime. byID maps node ID -> node; nameIndex maps a simple symbol name
	// -> the node IDs that share it (for O(1) name resolution). indexOnce
	// guards their one-time construction.
	byID      map[string]domain.Node
	nameIndex map[string][]string
	indexOnce sync.Once
}

// FromIndex builds a canonical domain.Graph from a v1 index.Index: every symbol
// becomes a "symbol" node, call/caller edges "calls", inheritance edges
// "inherits" (with the tag prefix parsed off), imports "imports"; each package
// becomes a "module" node and each distinct source file a "file" node linked by
// "contains"/"defines" edges. The result is deterministic.
func FromIndex(ix *index.Index) Graph {
	g := domain.Graph{
		Project: domain.Project{Root: ix.Root},
	}
	entries := map[string]bool{}

	// Scope symbol node IDs by package so same-named symbols in different
	// packages don't collide (e.g. two "func Save" in different packages used to
	// both become node "Save"). packagePathByFile maps each file to its package
	// path, falling back to the file's directory; the root package (".") keeps
	// bare node IDs.
	pkgByFile := packagePathByFile(ix)
	nodeID := func(s index.Symbol) string {
		p := pkgByFile[s.File]
		if p == "" {
			p = filepath.Dir(s.File)
		}
		if p == "" || p == "." {
			return s.FullName()
		}
		return p + "." + s.FullName()
	}

	// Capture framework entry-point metadata (handler, route, etc.).
	for _, s := range ix.Symbols {
		id := nodeID(s)
		ds := domain.FromIndexSymbol(s)
		g.Nodes = append(g.Nodes, domain.Node{
			ID:     id,
			Kind:   "symbol",
			Label:  id,
			Symbol: &ds,
		})
		if s.Entry || s.Route != "" || s.Framework != "" {
			entries[id] = true
		}
	}

	// Emit distinct file nodes (in symbol iteration order) plus their
	// contains/defines edges.
	seenFiles := map[string]bool{}
	for _, s := range ix.Symbols {
		fp := s.File
		if fp == "" {
			continue
		}
		if !seenFiles[fp] {
			seenFiles[fp] = true
			g.Nodes = append(g.Nodes, domain.Node{
				ID:    "file:" + fp,
				Kind:  "file",
				Label: fp,
				File:  &domain.File{Path: fp},
			})
		}
		g.Edges = append(g.Edges, domain.Edge{From: "file:" + fp, To: nodeID(s), Kind: "contains"})
		g.Edges = append(g.Edges, domain.Edge{From: nodeID(s), To: "file:" + fp, Kind: "defines"})
	}

	for path, pkg := range ix.Pkgs {
		lang := pkg.Lang
		g.Nodes = append(g.Nodes, domain.Node{
			ID:    path,
			Kind:  "module",
			Label: path,
			File:  &domain.File{Path: path, Language: lang},
		})
	}

	// Call edges: caller -> callee.
	for from, tos := range ix.Calls {
		for _, to := range tos {
			g.Edges = append(g.Edges, domain.Edge{From: from, To: to, Kind: "calls"})
		}
	}

	// Inheritance edges: subtype -> base, tagged kind parsed from the prefix.
	for sub, bases := range ix.Inherits {
		for _, tagged := range bases {
			g.Edges = append(g.Edges, domain.Edge{
				From: sub,
				To:   inheritBase(tagged),
				Kind: inheritKind(tagged),
			})
		}
	}

	// Import edges: package -> imported package.
	for path, pkg := range ix.Pkgs {
		for _, imp := range pkg.Imports {
			g.Edges = append(g.Edges, domain.Edge{From: path, To: imp, Kind: "imports"})
		}
	}

	// The v1 index is pure AST extraction, so confidence is 1.0.
	g.Provenance = &domain.Provenance{
		Source:      "ast",
		ExtractedAt: ix.UpdatedAt,
		Extractor:   "index",
		Confidence:  1.0,
	}

	g.Version = &domain.VersionMetadata{
		BuiltAt:     ix.UpdatedAt,
		SymbolCount: len(ix.Symbols),
		EdgeCount:   len(g.Edges),
		GraphHash:   graphHash(g),
	}

	return Graph{Graph: g, entries: entries}
}

// packagePathByFile maps each file in the index's package table to its package
// path. Files not listed in any package (e.g. when the table is sparse) are
// absent and resolved to their directory by the caller.
func packagePathByFile(ix *index.Index) map[string]string {
	m := make(map[string]string, len(ix.Pkgs))
	for path, pkg := range ix.Pkgs {
		for _, f := range pkg.Files {
			m[f] = path
		}
	}
	return m
}

// inheritKind returns the edge kind encoded in an inheritance-tagged base name
// like "extends:Animal". Defaults to "inherits" when no tag is present.
func inheritKind(tagged string) string {
	if i := strings.IndexByte(tagged, ':'); i >= 0 {
		return tagged[:i]
	}
	return "inherits"
}

// inheritBase returns the bare base name from a tagged value like
// "extends:Animal".
func inheritBase(tagged string) string {
	if i := strings.IndexByte(tagged, ':'); i >= 0 {
		return tagged[i+1:]
	}
	return tagged
}

// graphHash computes a stable SHA-256 over the sorted node IDs and sorted edge
// keys of the graph. Deterministic: equal graphs always hash equal.
func graphHash(g domain.Graph) string {
	ids := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)

	keys := make([]string, 0, len(g.Edges))
	for _, e := range g.Edges {
		keys = append(keys, e.From+"\x00"+e.Kind+"\x00"+e.To)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
