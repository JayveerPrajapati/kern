package intel

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// This file is the characterization oracle for the edge-incidence indexes
// that replaced the per-query full-edge scans (2026-10-28 perf backlog item
// 1). calleeIsTarget is the ORIGINAL, unoptimized definition of "does this
// call edge's callee endpoint name the target?" — moved here verbatim so it
// is part of the test surface only (the graph LOC cap is a production-code
// budget; the implementation is now calleeCandidates + calleeIndex). It is
// deliberately kept byte-for-byte so the parity tests pin the optimized path
// against the exact semantics that shipped before the consolidation.
//
// DO NOT "fix" this function to match calleeCandidates. A failure here means
// the optimized candidate extraction diverged and the production code must be
// reconciled.

// calleeIsTarget reports whether a "calls" edge's callee endpoint names the
// target node. The direct/import-qualified resolution (ResolveEdgeEndpoint)
// is tried first; when its caller pre-condition fails — the caller endpoint
// is an ambiguous bare name, or the callee is a bare name with no qualifier
// to link — two package-aware fallbacks run, anchored on the caller's own
// package and on the target's own package respectively. A foreign or
// unresolvable callee (e.g. "fmt.Println", "buf.Save") never matches.
func (g *Graph) calleeIsTarget(e domain.Edge, target string) bool {
	if e.Resolution != "" {
		return e.To == target
	}
	// A foreign-qualified callee never names a local symbol, even when the
	// local bare name is unique: the unique-simple-name resolution
	// (resolveNodeID case 1) would otherwise attribute every stdlib/vendor
	// method call sharing the name to the one project symbol that defines it
	// (the Go stdlib http.DefaultClient.Do call in TestV1EventsStreamSSE was
	// attributed to internal/mcp/highlevel.Do). Mirrors the index's
	// TestForeignCalleeNeverAliasesLocalSymbol contract.
	g.initIndex()
	if g.foreignCallee(e.To) {
		return false
	}
	// A bare callee that is a Go predeclared identifier ("append", "len")
	// binds to the builtin unless the CALLER's package shadows it with a
	// package-level definition (Go scoping; methods are never bare-callable,
	// so a method named "append" never shadows the builtin). The
	// project-wide unique-name resolution must not capture every builtin
	// call for the one project symbol that happens to define the name
	// (internal/gates Store.append): only a same-package receiver-less
	// definition can be the callee.
	if !strings.Contains(e.To, ".") && goPredeclared[e.To] {
		if callerID, ok := g.resolveNodeID(e.From); ok {
			if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok {
				if tn, ok2 := g.byID[id]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" {
					return false
				}
				return id == target
			}
		}
		return false
	}
	// A bare callee from a receiver-qualified caller is resolved
	// package-aware FIRST: inside a method, a bare reference resolves to the
	// caller's own package — a package-level func in Go, or the receiver
	// type's method in TS (the extractor records `this.get(...)` as a bare
	// "get"). Without this the unique receiver-less definition project-wide
	// (the internal/web helper "get") hijacks every Client method's call,
	// and impact reports the TS Client.get callers against the web helper
	// instead of the method. Every receiver-matched candidate is tried (the
	// caller key "Client.audit" is pooled by the Python and TS SDKs); if a
	// candidate's package resolves the callee, that resolution is
	// authoritative for the edge.
	if !strings.Contains(e.To, ".") {
		if qual, bare := endpointParts(e.From); qual != "" && bare != "" {
			resolved := false
			for _, cid := range g.nameIndex[bare] {
				n, ok := g.byID[cid]
				if !ok || n.Symbol == nil || n.Symbol.Receiver != qual {
					continue
				}
				if id, ok := g.resolveEndpointPackageAware(e.To, cid, false, false); ok {
					resolved = true
					if id == target {
						// A BARE reference never names a method under GO
						// SCOPING (a bare call binds to a package-level func
						// or the builtin). The caller-anchored resolution of a
						// bare callee must not land on a same-named method —
						// the import-based fallback below would otherwise
						// attribute the local closure "add" inside
						// TaskService.impactCitedFiles to
						// PlaybookStore.add. Go-only, mirroring the
						// ResolveEdgeEndpoint branch: in TS/Python/Java a bare
						// reference CAN name a method.
						if tn, ok2 := g.byID[id]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" && tn.Symbol.Language == "go" {
							continue
						}
						return true
					}
				}
			}
			if resolved {
				return false
			}
		}
	}
	if toID, ok := g.ResolveEdgeEndpoint(e.To, e.From); ok {
		if toID == target {
			// A BARE reference never names a method under GO SCOPING (a bare
			// call binds to a package-level func or the builtin). The
			// unique-simple-name resolution must not capture every bare
			// call for a method that happens to be the only symbol of its
			// name ("check" is unique because only Schema.check exists, but
			// CheckBoundariesPrecise's bare check() is not a call to it).
			// The rule is Go-only: in TS/Python/Java a bare reference CAN
			// name a method (`this.greet()` is recorded bare "greet" by the
			// extractor), so a foreign resolved method is a real callee
			// (A5, Phase 5 — verified per-language by
			// TestParityReportPerLanguage's guard model).
			if !strings.Contains(e.To, ".") {
				if tn, ok2 := g.byID[toID]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" && tn.Symbol.Language == "go" {
					return false
				}
			}
			return true
		}
		// A node-ID direct-hit on the raw endpoint (a root-package symbol
		// whose bare node ID collides with the reference — the CHANGELOG.md
		// heading "Changelog" is a node whose ID is the bare name) resolved
		// the callee WITHOUT disambiguation. That resolution is provisional:
		// fall through to the package-aware fallbacks below, which anchor on
		// the caller's package and only match when the endpoint genuinely
		// names the target (the Go tests call package-local Changelog-style
		// helpers, never the markdown heading). Any other (import-qualified /
		// unique-name) resolution is authoritative: a callee that resolved
		// to a different symbol does not call the target.
		if _, direct := g.byID[e.To]; !direct {
			return false
		}
	}
	g.initIndex()
	// A bare callee referenced from the caller's own package resolves to
	// the same-package candidate (a package holds at most one definition of
	// any name), so the target's own-package edges survive even though the
	// callee is a bare ambiguous name ("AuthorizeContext" from
	// authorize_test.go is the core, not the MCP wrapper).
	if callerID, ok := g.resolveNodeID(e.From); ok {
		if id, ok := g.resolveEndpointPackageAware(e.To, callerID, false, false); ok && id == target {
			// A BARE reference never names a method under GO SCOPING (a bare
			// call binds to a package-level func or the builtin). The
			// caller-anchored resolution of a bare callee must not land on
			// a same-named method (a bare "check" in a package importing
			// internal/schema is not Schema.check), or every import-linked
			// bare call would pool onto the method. Go-only, mirroring the
			// guard above: foreign receiver-less method edges (TS
			// `this.greet()`, Ruby file-owned `install`) are real callees
			// (A5, Phase 5).
			if !strings.Contains(e.To, ".") {
				if tn, ok2 := g.byID[id]; ok2 && tn.Symbol != nil && tn.Symbol.Receiver != "" && tn.Symbol.Language == "go" {
					return false
				}
			}
			return true
		}
		// The package-aware resolution attributed the callee to a DIFFERENT
		// same-named node (the caller's own package wins). That is correct
		// for a bare ambiguous callee, but a receiver-qualified endpoint
		// ("Server.dispatch") still names this target when its qualifier is
		// the target's receiver — fall through to that check instead of
		// dropping the edge (the index attributes such edges to every
		// same-named method, and explore reports them for each).
	}
	// An import-qualified callee ("governance.AuthorizeContext") whose
	// caller is itself an ambiguous bare name (New, Generate): the qualifier
	// names the target's own package, so the callee IS the target — the edge
	// must not be dropped on the caller pre-condition. The qualifier is a
	// PACKAGE name, so it can only name a package-level (receiver-less)
	// symbol: "tokenize.Count" is the func Count's qualified form and must
	// not be base-matched onto the method Estimator.Count just because both
	// live in internal/tokenize.
	if i := strings.LastIndexByte(e.To, '.'); i > 0 && i < len(e.To)-1 {
		targetPkg := g.nodePkg[target]
		if targetPkg != "" && e.To[:i] == lastPathSeg(targetPkg) {
			if tn, ok := g.byID[target]; ok && tn.Symbol != nil && tn.Symbol.Receiver != "" {
				return false
			}
			for _, id := range g.nameIndex[e.To[i+1:]] {
				if id == target {
					return true
				}
			}
		}
	}
	// A receiver-qualified callee ("Server.dispatch") whose caller is itself
	// an ambiguous receiver-qualified name ("Server.Serve"): neither side
	// carries package context, but the endpoint names the target's own
	// receiver + name, so the edge calls the target. Without this, impact
	// reported 0 callers for a method whose edges explore resolves (both
	// endpoints are ambiguous whenever two packages define the same method
	// name on the same receiver type).
	if g.endpointNamesNode(e.To, target) {
		return true
	}
	return false
}
