// PlatformAPI is the surface TaskService (and the moved task cluster) needs
// from the platform composition root. *app.Platform satisfies it structurally
// (internal/app/platform.go carries `var _ tasklife.PlatformAPI =
// (*Platform)(nil)`); tasklife never imports internal/app. The interface
// captures exactly the methods the moved code calls — anything the platform
// exposes beyond this surface is invisible to the cluster.
package tasklife

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// PlatformAPI is the platform surface the task-lifecycle cluster consumes.
// The composition root (internal/app.Platform) satisfies it without method
// changes; the interface is the ONLY coupling between the two packages.
type PlatformAPI interface {
	// Root returns the project root the platform was built for.
	Root() string
	// Index returns the shared prebuilt index (read-only).
	Index() *index.Index
	// Graph returns the shared twin-merged knowledge graph (read-only).
	Graph() *intel.Graph
	// Memory returns the shared engineering memory store.
	Memory() *memory.MemoryStore
	// Firewall returns the shared governance firewall.
	Firewall() *governance.Firewall
	// RuntimeSource returns the optional runtime source (may be nil).
	RuntimeSource() runtime.Source
	// ContextEngine returns the shared context engine.
	ContextEngine() *context.Engine
	// Analyze runs the context engine against a proposed change.
	Analyze(change string) (domain.ContextPacket, string, error)
	// Risk runs the context engine and returns the focused risk view.
	Risk(change string) (domain.ContextPacket, string, error)
	// WhatIf simulates a hypothetical change against the knowledge graph.
	WhatIf(kind whatif.ChangeKind, change, newTarget string) (whatif.Impact, string, error)
	// Verify runs the verification engine against the requested types.
	Verify(types []string, opts ...verification.Option) verdict.VerificationResult
	// CorrelateCode runs the incident→twin→code correlation.
	CorrelateCode(alert domain.Alert) (Correlation, error)
	// CodeContext assembles the grounded project context for the coder.
	CodeContext(intent, plan string) (string, error)
}

// resolveSymbol normalizes a change description into a bare symbol name. It is
// the tasklife-side copy of the platform's resolver (moved with the cluster so
// tasklife stays self-contained); it operates on the PlatformAPI surface only.
func resolveSymbol(api PlatformAPI, change string) (string, bool, error) {
	if !strings.ContainsAny(change, " \t") {
		if api.Graph() == nil || api.Graph().Resolvable(change) {
			return change, false, nil
		}
		// intel.ResolveEntry — the same entry resolver kern explore uses —
		// accepts references the graph resolver alone rejects, notably a bare
		// method name shared by several receivers ("dispatch" ->
		// "Server.dispatch"), and applies the full refinement explore applies:
		// a bare name that first lands on a test or fixture symbol (the
		// fixture store.New beating the production app.New — P2 entry parity)
		// is re-pointed at a production definition, and a package qualifier
		// is honored. The refinement happens BEFORE the mapping loop below so
		// the graph check gates the refined result. Map the answer to a form
		// the graph accepts: the FullName, its bare name, or the
		// package-scoped node ID. full may equal the query for a bare name
		// defined in several packages (a bare func's FullName is its bare
		// name); the package-scoped node ID still disambiguates it, so it is
		// tried even then — with non-testdata definitions preferred (Fix 3).
		// The ambiguity itself stays visible via the render's definition NOTE.
		if api.Index() != nil {
			if _, d, ok := intel.ResolveEntry(api.Index(), change); ok && d != nil {
				full := d.FullName()
				for _, cand := range []string{full, symbolBareName(full), packageScopedID(api.Index(), full, change)} {
					if cand != "" && cand != change && api.Graph().Resolvable(cand) {
						return cand, true, nil
					}
				}
			}
		}
		return "", false, fmt.Errorf("no symbol named %q was found in this project's index (closest candidates: %s): kern what-if analyzes an existing symbol — pass its exact name, or a qualified 'pkg.Symbol'",
			change, closestCandidates(api.Index(), change))
	}
	cands := whatif.ExtractSymbolsIndex(change, api.Index())
	// Prefer the first candidate that exists in the graph; keep extraction
	// order as the tiebreaker.
	if api.Graph() != nil {
		for _, c := range cands {
			if api.Graph().Resolvable(c) {
				return c, false, nil
			}
		}
		// A candidate that IS a real index symbol but whose bare name the
		// graph rejects (ambiguous — several same-named defs) must still beat
		// a whole-request ranked search: mirror the single-token path's
		// intel.Resolve mapping so "what breaks if I change dispatch"
		// resolves to a production dispatch, never to an unrelated symbol
		// (I2). Without this, the ranked search over the filler-laden request
		// surfaced TestWhatIfRequiresChange — a test func sharing the
		// request's "what if ... change" words. Bare package funcs carry the
		// bare name as FullName, so also try the package-scoped node ID
		// directly ("dispatch" -> "a.dispatch") when Resolve adds nothing.
		if api.Index() != nil {
			for _, c := range cands {
				if full, ok := intel.Resolve(api.Index(), c); ok && full != c {
					for _, cand := range []string{full, symbolBareName(full), packageScopedID(api.Index(), full, c)} {
						if cand != "" && cand != c && api.Graph().Resolvable(cand) {
							return cand, true, nil
						}
					}
				}
				if scoped := packageScopedID(api.Index(), c, c); scoped != "" && scoped != c && api.Graph().Resolvable(scoped) {
					return scoped, true, nil
				}
			}
		}
		// Try high-confidence ranked search match for approximate phrases.
		// Test symbols (_test.go files, Test*/Benchmark* names) and testdata
		// fixtures must not outrank production symbols: a request like "what
		// breaks if I change dispatch" matched TestWhatIfRequiresChange before
		// the real production symbols (I2), and "dispatch" matched the testdata
		// stub before the production definition (Fix 3). Prefer the first
		// resolvable production hit; use a test/testdata symbol only when no
		// production symbol resolved.
		if api.Index() != nil {
			var testFallback string
			for _, h := range intel.RankedSearchScored(api.Index(), change, 5) {
				if h.Score < 150 {
					continue
				}
				if intel.IsTestFile(h.Symbol.File) ||
					intel.IsFixtureFile(h.Symbol.File) ||
					strings.HasPrefix(h.Symbol.Name, "Test") ||
					strings.HasPrefix(h.Symbol.Name, "Benchmark") {
					if testFallback == "" {
						if api.Graph().Resolvable(h.Symbol.FullName()) {
							testFallback = h.Symbol.FullName()
						} else if api.Graph().Resolvable(h.Symbol.Name) {
							testFallback = h.Symbol.Name
						}
					}
					continue
				}
				if api.Graph().Resolvable(h.Symbol.FullName()) {
					return h.Symbol.FullName(), true, nil
				}
				if api.Graph().Resolvable(h.Symbol.Name) {
					return h.Symbol.Name, true, nil
				}
			}
			if testFallback != "" {
				return testFallback, true, nil
			}
		}
		if len(cands) == 0 {
			return "", false, fmt.Errorf("could not identify a symbol in the change description: pass a bare symbol name (e.g. 'GetMySQLDB') or include a qualified name (e.g. 'pkg.Symbol') in the description")
		}
		return "", false, fmt.Errorf("no symbol named %q was found in this project's index (closest candidates: %s): kern what-if analyzes an existing symbol — pass its exact name, or a qualified 'pkg.Symbol'",
			cands[0], strings.Join(cands, ", "))
	}
	if len(cands) == 0 {
		return "", false, fmt.Errorf("could not identify a symbol in the change description: pass a bare symbol name (e.g. 'GetMySQLDB') or include a qualified name (e.g. 'pkg.Symbol') in the description")
	}
	return cands[0], false, nil
}

// closestCandidates returns the top ranked-search symbol names near change,
// for the "no symbol named ..." error message ("" when no index).
func closestCandidates(ix *index.Index, change string) string {
	if ix == nil {
		return ""
	}
	var names []string
	for _, h := range intel.RankedSearchScored(ix, change, 3) {
		names = append(names, h.Symbol.Name)
	}
	return strings.Join(names, ", ")
}

// symbolBareName returns the part of a qualified name after the last '.'
// ("Server.dispatch" -> "dispatch"; a plain name is returned unchanged).
func symbolBareName(full string) string {
	if i := strings.LastIndexByte(full, '.'); i >= 0 {
		return full[i+1:]
	}
	return full
}

// packageScopedID derives the graph's package-scoped node ID
// ("<pkg>.<FullName>", bare FullName for the root package) for a symbol
// carrying the given FullName, mirroring intel.FromIndex's node ID
// derivation. It lets a name the plain graph resolver rejects (a same-named
// symbol in several packages, qualified by the user) still reach its defining
// node. When the original query is path-qualified ("bpcli/mcp.NewServer"),
// the first symbol defined under a matching path suffix wins, so the user's
// qualifier — not index order — picks the package.
func packageScopedID(ix *index.Index, full, query string) string {
	if ix == nil {
		return ""
	}
	pkgByFile := make(map[string]string, len(ix.Pkgs))
	for path, pk := range ix.Pkgs {
		for _, f := range pk.Files {
			pkgByFile[f] = path
		}
	}
	// dirPrefix is the query's qualifier before its last separator
	// ("bpcli/mcp" in "bpcli/mcp.NewServer"); "" for a bare name.
	dirPrefix := ""
	if i := strings.LastIndexByte(query, '.'); i >= 0 {
		dirPrefix = query[:i]
	} else if i := strings.LastIndexByte(query, '/'); i >= 0 {
		dirPrefix = query[:i]
	}
	var firstID, firstNonProdID string
	for _, s := range ix.Symbols {
		if s.FullName() != full {
			continue
		}
		pkg := pkgByFile[s.File]
		if pkg == "" {
			pkg = filepath.Dir(s.File)
		}
		id := full
		if pkg != "" && pkg != "." {
			id = pkg + "." + full
		}
		// Test and fixture symbols (Fix 3 / P2 entry parity): a name defined
		// in BOTH production and a test/fixture symbol must resolve to the
		// production definition — the live defects were "dispatch" resolving
		// to the stub in testdata/resolve_prio/a/a.go and "New" resolving to
		// the stub in evaluate/*/fixture/store/store.go. The predicate is
		// intel.IsNonProduction — the SAME one the entry resolver uses — so
		// a /fixture/ directory is demoted exactly like explore demotes it
		// (the exported intel.IsFixtureFile only covers /testdata/ and would
		// let the store.New fixture win the node-ID mapping). A non-production
		// symbol wins only when it is the ONLY definition (the render
		// annotates that case).
		if intel.IsNonProduction(s) {
			if firstNonProdID == "" {
				firstNonProdID = id
			}
			continue
		}
		if firstID == "" {
			firstID = id
		}
		if dirPrefix != "" && fileMatchesPathPrefix(s.File, dirPrefix) {
			return id
		}
	}
	if firstID != "" {
		return firstID
	}
	return firstNonProdID
}

// fileMatchesPathPrefix reports whether the directory part of file ends
// with the '/'-separated segments of prefix ("internal/bpcli/mcp/server.go"
// carries the prefix "bpcli/mcp").
func fileMatchesPathPrefix(file, prefix string) bool {
	if prefix == "" {
		return true
	}
	segs := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(segs) == 0 {
		return true
	}
	fsegs := strings.Split(strings.Trim(filepath.Dir(file), "/"), "/")
	if len(fsegs) < len(segs) {
		return false
	}
	off := len(fsegs) - len(segs)
	for i, s := range segs {
		if fsegs[off+i] != s {
			return false
		}
	}
	return true
}

// graphNodeFile returns the defining file of the graph node for the given
// reference, or "" when no such node exists. The reference may be a bare
// symbol name ("NewFileStore" — how resolveSymbol returns it) or a
// package-scoped node ID ("internal/governance.NewFileStore"); both forms
// are matched against the node ID and the symbol's qualified name. It is the
// tasklife-side copy of the platform helper (moved with the cluster).
func graphNodeFile(g *intel.Graph, ref string) string {
	for _, n := range g.Nodes {
		if n.ID == ref {
			if n.Symbol != nil {
				return n.Symbol.File
			}
			if n.File != nil {
				return n.File.Path
			}
			return ""
		}
		if n.Symbol != nil && n.Symbol.Qualified == ref && n.Symbol.File != "" {
			// A bare qualified form can match several same-named symbols in
			// different packages; only return when unambiguous.
			dup := false
			for _, m := range g.Nodes {
				if m.ID != n.ID && m.Symbol != nil && m.Symbol.Qualified == ref {
					dup = true
					break
				}
			}
			if !dup {
				return n.Symbol.File
			}
			return ""
		}
	}
	return ""
}

// testdataFixtureNote reports whether target's defining file is a testdata
// fixture, for the "resolve but annotate" contract (Fix 3): resolution
// prefers non-testdata definitions; when every definition of a name lives
// under testdata/, resolution still succeeds but the answer must say it is a
// fixture, not production code. Returns "" for production targets.
func testdataFixtureNote(g *intel.Graph, target string) string {
	if g == nil || target == "" {
		return ""
	}
	f := graphNodeFile(g, target)
	if f == "" || !intel.IsFixtureFile(f) {
		return ""
	}
	return "(testdata fixture)"
}
