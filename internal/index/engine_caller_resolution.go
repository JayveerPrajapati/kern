package index

import (
	"path/filepath"
	"strings"
)

func (ix *Index) filePkgIndex() map[string]string {
	out := map[string]string{}
	for path, p := range ix.Pkgs {
		for _, f := range p.Files {
			if _, dup := out[f]; !dup {
				out[f] = path
			}
		}
	}
	return out
}

// callerImportsDef reports whether a RESOLVABLE caller's package imports the
// def's package under qual (the callee's recorded qualifier), mirroring the
// graph's import-based resolution of a qualified callee through the caller's
// own imports (resolveImportQualified). A caller in a package that does not
// import the def's package is not a caller of this def: the base-name match
// alone would let "governance.AuthorizeContext" callers of the core
// internal/governance leak onto the MCP wrapper in internal/mcp/governance,
// which merely shares the final path segment.
func (ix *Index) callerImportsDef(caller string, d Symbol, qual string) bool {
	defs := ix.symbolsFor(caller)
	if len(defs) != 1 {
		return false // unresolvable callers join via the graph's fallback instead
	}
	filePkg := ix.callerIndex().filePkg
	callerPkg := filePkg[defs[0].File]
	if callerPkg == "" {
		return false
	}
	p := ix.Pkgs[callerPkg]
	if p == nil {
		return false
	}
	defPkg := filePkg[d.File]
	if defPkg == "" {
		return false
	}
	for _, imp := range p.Imports {
		path := strings.Trim(imp.Path, `"' `)
		if path == "" {
			continue
		}
		seg := path[strings.LastIndexByte(path, '/')+1:]
		if seg != qual {
			continue
		}
		if path == defPkg || strings.HasSuffix(path, "/"+defPkg) {
			return true
		}
	}
	return false
}

// callerAttributedBare mirrors the graph's callee-side resolution of a bare
// callee (CallersFor's bare-bucket gate, shared by the method branch and the
// shared-name func path):
//   - a receiver-qualified caller ("Client.audit") is resolved package-aware
//     over every receiver-matched candidate, so the same-package def wins
//     (callerHasLocalDef);
//   - a bare caller needs an unambiguous resolution — every def must live in
//     this package (callerInPackage);
//   - either form is additionally attributed when the caller's package
//     imports this def's package (callerImportsDefPkg);
//   - a caller that IS the def itself (a self-edge recorded through a
//     chained/receiver form — "Schema.check" -> "Schema.check") is never a
//     caller.
func (ix *Index) callerAttributedBare(caller string, s Symbol, dir string) bool {
	if caller == s.FullName() {
		return false
	}
	if strings.Contains(caller, ".") {
		return callerHasLocalDef(ix, caller, dir) || ix.callerImportsDefPkg(caller, s)
	}
	// A bare caller whose name has a UNIQUE receiver-less definition
	// project-wide resolves to that definition — the graph's resolveNodeID
	// qualified-match (a receiver-less func's Qualified IS the bare name, so
	// it wins even when methods share the simple name; e.g. tokenize.Count
	// with five Count methods beside the func). Mirror the graph's
	// caller-anchored callee resolution (resolveEndpointPackageAware): the
	// bare callee resolves with the same-package preference anchored on the
	// CALLER's unique definition, so the edge is s only when s lives in that
	// definition's package. callerHasLocalDef would be wrong here — it counts
	// any same-named def (methods included) in dir, while the graph resolves
	// the caller to its unique receiver-less definition alone. Without this
	// branch, a shared caller name dropped real same-package callers (Phase 5
	// Part C: tokenize.Default's caller Count — Count() literally calls
	// Default()); and without the receiver-less-only check it would
	// over-attribute (mcp/etag's Registry.Count method must not make the
	// tokenize Count a caller of mcp/etag.Default). The import-based
	// attribution is anchored the same way (ADV-4): only the unique
	// receiver-less definition's package imports may attribute the edge —
	// a same-named method's package imports must not (callerImportsDefPkg
	// consults every same-named def, methods included, so the branch uses
	// the anchored form).
	if uniqueReceiverLessDef(ix, caller) {
		for _, cd := range ix.symbolsFor(caller) {
			if cd.Receiver == "" {
				if filepath.Dir(cd.File) == dir {
					return true
				}
				return ix.callerImportsDefPkgAnchored(cd, s)
			}
		}
		return false
	}
	return callerInPackage(ix, caller, dir) || ix.callerImportsDefPkg(caller, s)
}

// callerImportsDefPkg mirrors the graph's import-based resolution of a BARE
// callee (resolveEndpointPackageAware's import branch): a caller in a package
// that does NOT itself define the bare name still calls this def when its
// package imports the def's package (the internal/web rate-limit test drives
// the SDK's Client.do through an HTTP client). Same-package callers are
// handled by callerHasLocalDef.
func (ix *Index) callerImportsDefPkg(caller string, s Symbol) bool {
	// A bare callee with a predeclared identifier's name ("append", "len")
	// is the builtin unless the caller's package shadows it with a
	// package-level definition — never an import of a package defining a
	// same-named symbol (Go scoping; methods are not bare-callable).
	if isPredeclared(s.Name) {
		return false
	}
	// A bare callee can never name a METHOD: a bare reference resolves to a
	// package-level func or the builtin (Go scoping) — an import of the
	// method's package must not capture the call (a local closure named
	// "do" in internal/web is not the SDK's Client.do).
	if s.Receiver != "" {
		return false
	}
	callerDefs := ix.symbolsFor(caller)
	if len(callerDefs) == 0 {
		return false
	}
	// The caller's package must not define the bare name itself: the graph's
	// same-package preference resolves the bare callee to that local
	// definition, never to s.
	for _, cd := range callerDefs {
		cdir := filepath.Dir(cd.File)
		for _, d := range ix.symbolsFor(s.Name) {
			if filepath.Dir(d.File) == cdir {
				return false
			}
		}
	}
	filePkg := ix.callerIndex().filePkg
	defPkg := filePkg[s.File]
	if defPkg == "" {
		return false
	}
	for _, cd := range callerDefs {
		callerPkg := filePkg[cd.File]
		if callerPkg == "" || callerPkg == defPkg {
			continue
		}
		p := ix.Pkgs[callerPkg]
		if p == nil {
			continue
		}
		if pkgImportsDef(p, defPkg) {
			return true
		}
	}
	return false
}

// pkgImportsDef reports whether a package's import list contains the def's
// package (exact import path or repo-relative suffix), mirroring the graph's
// importMatchesQualifier.
func pkgImportsDef(p *Pkg, defPkg string) bool {
	for _, imp := range p.Imports {
		path := strings.Trim(imp.Path, `"' `)
		if path == "" {
			continue
		}
		if path == defPkg || strings.HasSuffix(path, "/"+defPkg) {
			return true
		}
	}
	return false
}

// callerImportsDefPkgAnchored is callerImportsDefPkg anchored on ONE caller
// definition (ADV-4): the import attribution runs only against the caller's
// unique receiver-less definition, never against its same-named methods. The
// graph resolves a bare caller with a unique receiver-less definition to that
// definition alone (resolveNodeID's qualified-match), so only ITS package's
// imports may attribute a bare edge to s — and only ITS package's local
// definitions may shadow the bare callee (Go same-package preference). The
// unanchored callerImportsDefPkg keeps the all-defs semantics for callers
// that remain ambiguous.
func (ix *Index) callerImportsDefPkgAnchored(callerDef Symbol, s Symbol) bool {
	// A bare callee with a predeclared identifier's name ("append", "len")
	// is the builtin unless the caller's package shadows it with a
	// package-level definition — never an import of a package defining a
	// same-named symbol (Go scoping; methods are not bare-callable).
	if isPredeclared(s.Name) {
		return false
	}
	// A bare callee can never name a METHOD: a bare reference resolves to a
	// package-level func or the builtin (Go scoping) — an import of the
	// method's package must not capture the call (a local closure named
	// "do" in internal/web is not the SDK's Client.do).
	if s.Receiver != "" {
		return false
	}
	// The caller's package must not define the bare name itself: the graph's
	// same-package preference resolves the bare callee to that local
	// definition, never to s. Checked for the anchored definition's package
	// alone — a same-named method's package cannot shadow the anchored
	// caller's bare reference.
	cdir := filepath.Dir(callerDef.File)
	for _, d := range ix.symbolsFor(s.Name) {
		if filepath.Dir(d.File) == cdir {
			return false
		}
	}
	filePkg := ix.callerIndex().filePkg
	defPkg := filePkg[s.File]
	if defPkg == "" {
		return false
	}
	callerPkg := filePkg[callerDef.File]
	if callerPkg == "" || callerPkg == defPkg {
		return false
	}
	p := ix.Pkgs[callerPkg]
	if p == nil {
		return false
	}
	return pkgImportsDef(p, defPkg)
}

// CallersOfName returns callers for a possibly-unknown name (a foreign target
// like "fmt.Println", or an unresolved receiver call like "v.M"). Exact
// entries win; simple-name aliases are only consulted when the name matches
// no local symbol, so a local Println never inherits callers of fmt.Println.
