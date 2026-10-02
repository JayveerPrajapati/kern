// PlatformAPI is the surface TaskService (and the moved task cluster) needs
// from the platform composition root. *app.Platform satisfies it structurally
// (internal/app/platform.go carries `var _ tasklife.PlatformAPI =
// (*Platform)(nil)`); tasklife never imports internal/app. The interface
// captures exactly the methods the moved code calls — anything the platform
// exposes beyond this surface is invisible to the cluster.
package tasklife

import (
	"fmt"
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
		// If single-token symbol not directly resolvable, find the closest
		// candidate via ranked search. Only a match that covers EVERY query
		// word counts: a partial segment hit (e.g. "NoSuchSymbolXYZ" sharing
		// the words "no"/"symbol" with an unrelated symbol) must NOT silently
		// substitute that symbol.
		if api.Index() != nil {
			if cand, ok := intel.ResolveFuzzy(api.Index(), change); ok && api.Graph().Resolvable(cand) {
				return cand, true, nil
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
		// Try high-confidence ranked search match for approximate phrases.
		if api.Index() != nil {
			for _, h := range intel.RankedSearchScored(api.Index(), change, 5) {
				if h.Score >= 150 {
					if api.Graph().Resolvable(h.Symbol.FullName()) {
						return h.Symbol.FullName(), true, nil
					}
					if api.Graph().Resolvable(h.Symbol.Name) {
						return h.Symbol.Name, true, nil
					}
				}
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
