package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// callerIndex is the lazily-built reverse-edge index behind CallersFor and
// the sourced-caller buckets, precomputed ONCE per index so per-symbol
// lookups stop rescanning the whole Calls/Callers/Pkgs maps on every query.
// The blast-radius walk (internal/intel blastRadiusWalk) calls
// CallersFor once per visited node; the unique-name widening inside
// callersForLegacy used to rescan ALL of ix.Callers (plus recompute
// foreignImportBases and filePkgIndex) per node — the measured ~900x
// latency regression vs docs/benchmarks/graph-latency.md's documented
// numbers. All four tables are pure functions of the finalized index (maps
// are never mutated after finalize), so the once-build is safe and the
// cached values are bit-identical to the recomputed ones.
type callerIndex struct {
	foreignBases    map[string]bool                // foreignImportBases, computed once
	filePkg         map[string]string              // filePkgIndex, computed once
	bySimpleKey     map[string]map[string][]string // ix.Callers grouped by simpleKey(k)
	sourcedBySimple map[string][]string            // callers of sourced edges, by simpleName(edge.Target)
}

// callerIndex returns the index's lazily-built reverse-edge tables, building
// them once from the finalized index on first use (same sync.Once pattern as
// the binding catalog and symbol index).
func (ix *Index) callerIndex() *callerIndex {
	c := ix.getCache()
	c.callerOnce.Do(func() {
		ci := &callerIndex{
			foreignBases:    ix.foreignImportBases(),
			filePkg:         ix.filePkgIndex(),
			bySimpleKey:     map[string]map[string][]string{},
			sourcedBySimple: map[string][]string{},
		}
		for k, callers := range ix.Callers {
			sk := simpleKey(k)
			bucket := ci.bySimpleKey[sk]
			if bucket == nil {
				bucket = map[string][]string{}
				ci.bySimpleKey[sk] = bucket
			}
			bucket[k] = callers
		}
		// Sourced-caller buckets: every caller of an edge with source
		// provenance, keyed by the target's simple name — the exact index
		// intel's blast-radius walk used to rebuild from ALL of ix.Calls on
		// every invocation (internal/intel/intel.go). Deduplicated and sorted
		// for deterministic iteration.
		seen := map[string]map[string]bool{}
		for caller, edges := range ix.Calls {
			for _, e := range edges {
				if e.SourceFile == "" {
					continue
				}
				sk := simpleKey(e.Target)
				if seen[sk] == nil {
					seen[sk] = map[string]bool{}
				}
				seen[sk][caller] = true
			}
		}
		for sk, set := range seen {
			list := make([]string, 0, len(set))
			for caller := range set {
				list = append(list, caller)
			}
			ci.sourcedBySimple[sk] = dedupeSorted(list)
		}
		c.callerIdx = ci
	})
	return c.callerIdx
}

// SourcedCallersBySimple returns the callers of every call edge whose
// target's simple name is simple, limited to edges with source provenance
// (SourceFile != "") — the cached reverse-edge index intel's blast-radius
// walk consumes. Nil when no sourced edge targets that simple name.
func (ix *Index) SourcedCallersBySimple(simple string) []string {
	return ix.callerIndex().sourcedBySimple[simple]
}

// CallResStats counts distinct call targets and how many are unresolved,
// with the unresolved share split by cause (F3, production-readiness audit
// 2026-10-17): External targets are qualified by a foreign import base
// ("fmt.Println", vendor packages) — unresolvable by design and expected;
// InRepo is everything else — bare dynamic-language receivers,
// project-qualified targets that failed to resolve — the honest actionable
// share. Unresolved stays the sum so existing readers and the overall rate
// are unchanged. Zero External/InRepo on indexes built before the split
// (older readers then see the legacy unsplit render).
type CallResStats struct {
	Total      int `json:"total"`
	Unresolved int `json:"unresolved"`
	External   int `json:"external,omitempty"`
	InRepo     int `json:"in_repo,omitempty"`
	Dynamic    int `json:"dynamic,omitempty"`
}

// measureCallResolution counts distinct callee targets and how many fail
// resolveName, splitting unresolved targets by cause. Runs after
// computeCallers in every finalize/load sequence so `kern index --status`
// can report the honest per-repo resolution rate.
func (ix *Index) measureCallResolution() {
	seen := map[string]struct{}{}
	var external, inRepo, dynamic int
	// Foreign import bases ("fmt", "time") identify stdlib/vendor-qualified
	// targets the index cannot and should not resolve; recomputed per call
	// (same trust model as computeCallers, which runs before this pass).
	foreign := ix.foreignImportBases()
	pkgQualifiers := map[string]bool{}
	for path, pkg := range ix.Pkgs {
		if pkg != nil && pkg.Name != "" {
			pkgQualifiers[pkg.Name] = true
		}
		if base := filepath.Base(path); base != "" && base != "." {
			pkgQualifiers[base] = true
		}
	}
	// Receiver/type qualifiers are in-repo by definition: a miss like
	// Widget.Missing is a genuine resolution failure, while tt.status.String
	// (a variable/field receiver) is dynamic dispatch, not a miss.
	definedQualifiers := map[string]bool{}
	functionQualifiers := map[string]bool{}
	fieldQualifiers := map[string]bool{}
	for _, pkg := range ix.Pkgs {
		if pkg == nil {
			continue
		}
		for key := range pkg.StructFields {
			fieldQualifiers[key] = true
		}
	}
	for _, s := range ix.Symbols {
		if s.Receiver != "" {
			definedQualifiers[s.Receiver] = true
		}
		switch s.Kind {
		case "type", "struct", "interface", "class", "record", "enum":
			definedQualifiers[s.Name] = true
		case "func", "method", "function":
			functionQualifiers[s.Name] = true
		}
	}

	// Caller-aware import classification: a local directory named "http"
	// must not turn net/http calls into in-repo misses. For each caller we
	// know the file's imports, so a qualifier imported from outside the repo
	// is external even when it collides with a local package name.
	directories := map[string]bool{}
	pkgNames := map[string]string{}
	for dir, pkg := range ix.Pkgs {
		directories[filepath.ToSlash(dir)] = true
		if pkg != nil && pkg.Name != "" {
			pkgNames[filepath.ToSlash(dir)] = pkg.Name
		}
	}
	prefixes := moduleImportPrefixes(ix.Root)
	localImport := map[string]string{}
	localImportDir := func(path string) (string, bool) {
		if dir, ok := localImport[path]; ok {
			return dir, dir != ""
		}
		if len(prefixes) > 0 {
			// Go import paths inside the repo start with a module path. This
			// is exact: net/http stays external even when a local package
			// directory is also named "http".
			for _, prefix := range prefixes {
				if dir, ok := prefix.dirFor(path); ok {
					localImport[path] = dir
					return dir, true
				}
			}
			localImport[path] = ""
			return "", false
		}
		dir := matchImportDirectory(path, directories)
		if dir == "" {
			dir = matchImportByPackage(path, directories, pkgNames)
		}
		localImport[path] = dir
		return dir, dir != ""
	}
	type callerImportSets struct {
		local, foreign map[string]bool
		aliases        map[string]string
	}
	callerImports := map[string]callerImportSets{}
	addBase := func(set map[string]bool, base string) {
		if base == "" {
			return
		}
		set[base] = true
		if i := strings.IndexByte(base, '.'); i > 0 {
			set[base[:i]] = true
		}
	}
	for caller := range ix.Calls {
		sets := callerImportSets{local: map[string]bool{}, foreign: map[string]bool{}, aliases: map[string]string{}}
		for _, sym := range ix.symbolsFor(caller) {
			for _, imp := range ix.ImportsByFile[sym.File] {
				base := filepath.Base(imp.Path)
				alias := strings.TrimSpace(imp.Alias)
				if dir, ok := localImportDir(imp.Path); ok {
					addBase(sets.local, base)
					keys := []string{base, lastSegment(base)}
					if alias != "" && alias != "_" && alias != "." {
						sets.local[alias] = true
						keys = append(keys, alias)
					}
					if pkg := ix.Pkgs[dir]; pkg != nil && pkg.Name != "" {
						keys = append(keys, pkg.Name)
					}
					for _, key := range keys {
						if key != "" {
							sets.aliases[key] = dir
						}
					}
					continue
				}
				addBase(sets.foreign, base)
				if alias != "" && alias != "_" && alias != "." {
					sets.foreign[alias] = true
				}
			}
		}
		callerImports[caller] = sets
	}
	for caller, callees := range ix.Calls {
		sets := callerImports[caller]
		for _, ce := range callees {
			if _, dup := seen[ce.Target]; dup {
				continue
			}
			seen[ce.Target] = struct{}{}
			if _, ok := resolveName(ix, ce.Target); ok {
				continue
			}
			i := strings.Index(ce.Target, ".")
			qualifier, method := "", ""
			if i > 0 {
				qualifier = ce.Target[:i]
				method = ce.Target[i+1:]
			}
			if qualifier != "" {
				if dir, ok := sets.aliases[qualifier]; ok {
					if method != "" && !strings.Contains(method, ".") && ix.symbolInDir(method, dir) {
						continue // resolved through the caller's import alias
					}
					if method != "" && strings.Contains(method, ".") {
						dynamic++ // chained call on a value returned by the package
					} else {
						inRepo++
					}
					if os.Getenv("KERN_DEBUG_RESOLUTION") != "" {
						cause := "in-repo"
						if strings.Contains(method, ".") {
							cause = "dynamic/untyped"
						}
						fmt.Fprintf(os.Stderr, "unresolved (%s) target: %s\n", cause, ce.Target)
					}
					continue
				}
				if first := firstSegment(qualifier); first != qualifier {
					if _, ok := sets.aliases[first]; ok {
						inRepo++
						if os.Getenv("KERN_DEBUG_RESOLUTION") != "" {
							fmt.Fprintf(os.Stderr, "unresolved (in-repo) target: %s\n", ce.Target)
						}
						continue
					}
				}
			}
			cause := "dynamic/untyped"
			switch {
			case method != "" && strings.Contains(method, "."):
				// Chained member call (logger.Logger.Info, Component.mu.Lock,
				// common.GetRequestLogger.Debug): dynamic dispatch on a value,
				// not a package-qualified miss.
				dynamic++
			case qualifier != "" && functionQualifiers[qualifier]:
				// Call on a local function's return value (fn.Field.Method).
				dynamic++
			case qualifier != "" && fieldQualifiers[qualifier+"."+method]:
				// Struct field holding a func value (Component.InitFunc).
				dynamic++
			case qualifier != "" && sets.local[qualifier]:
				// The caller imports this qualifier from an in-repo package:
				// an unresolved call is a genuine package/type miss.
				inRepo++
				cause = "in-repo"
			case qualifier != "" && (sets.foreign[qualifier] || foreign[qualifier]):
				external++
				cause = "external"
			case qualifier != "" && (pkgQualifiers[qualifier] || definedQualifiers[qualifier] || definedQualifiers[lastSegment(qualifier)]):
				inRepo++
				cause = "in-repo"
			default:
				dynamic++
			}
			if os.Getenv("KERN_DEBUG_RESOLUTION") != "" {
				fmt.Fprintf(os.Stderr, "unresolved (%s) target: %s\n", cause, ce.Target)
			}
		}
	}
	ix.CallResolution = CallResStats{Total: len(seen), Unresolved: external + inRepo + dynamic, External: external, InRepo: inRepo, Dynamic: dynamic}
}

// lastSegment returns the final dot-separated segment of a qualifier
// ("logger.Logger" -> "Logger").
func lastSegment(qualifier string) string {
	if i := strings.LastIndexByte(qualifier, '.'); i >= 0 {
		return qualifier[i+1:]
	}
	return qualifier
}

// firstSegment returns the leading dot-separated segment of a qualifier
// ("common.Logger" -> "common").
func firstSegment(qualifier string) string {
	if i := strings.IndexByte(qualifier, '.'); i >= 0 {
		return qualifier[:i]
	}
	return qualifier
}

// symbolInDir reports whether name has a definition whose file lives in dir.
func (ix *Index) symbolInDir(name, dir string) bool {
	for _, s := range ix.symbolsFor(name) {
		if filepath.ToSlash(filepath.Dir(s.File)) == dir {
			return true
		}
	}
	return false
}

// methodLangFromFile resolves the language a method symbol's file must have
// when the mapping is unambiguous from the file path alone — the language of
// a .go file can only be "go" (goast is the sole Go extractor), a .py file
// only "python", and so on, mirroring detectLang's extension mapping.
// Content-dependent languages (.vue/.svelte script lang), .astro, and
// extensionless shebang scripts cannot be recovered from the path alone, so
// they return "" and the caller treats them as ambiguous.
func methodLangFromFile(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".vue", ".svelte", ".astro", "":
		return "" // content-dependent or extensionless: ambiguous without src
	}
	return langparse.DetectLang(rel, nil)
}

// enforceMethodLangInvariant guards the A5 bare-callee-never-methods rule's
// Go scoping (internal/intel/queries.go calleeIsTarget drops a bare callee
// only when the resolved method's Language == "go"): the rule is trustworthy
// only while every method symbol carries its language. goast stamps
// Lang:"go" on every Go symbol (goast.go) and the foreign extractors stamp
// the detected language, so Receiver != "" && Lang == "" can only come from a
// hand-built index or a broken extractor — which would silently re-open the
// over-attribution bug (a Go method missing its "go" tag escapes the guard
// and pools bare-reference callers). Unambiguous file-language fixup (.go ⇒
// "go", .py ⇒ "python", …) repairs the symbol; an ambiguous case (unknown
// extension, content-dependent language) fails loudly with a count rather
// than guessing. Runs once per finalize, at the head of computeCallers,
// before any pass reads Lang (computePrecisionByLang, the graph guard).
func (ix *Index) enforceMethodLangInvariant() {
	var offenders []string
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Receiver == "" || s.Lang != "" {
			continue
		}
		if lang := methodLangFromFile(s.File); lang != "" {
			s.Lang = lang
			continue
		}
		offenders = append(offenders, s.FullName()+" @ "+s.File)
	}
	if len(offenders) > 0 {
		panic(fmt.Sprintf("index invariant: %d method symbol(s) have Receiver != \"\" but no Lang (the A5 bare-callee guard needs every method's language); fix the extractor or set Lang explicitly: %s",
			len(offenders), strings.Join(offenders, "; ")))
	}
}

func (ix *Index) computeCallers() {
	// Load-bearing invariant (A5): every method symbol must carry its
	// language — the intel bare-callee-never-methods guard is scoped on
	// Lang == "go", so a method with Receiver != "" and Lang == "" would
	// silently re-open the over-attribution bug. Fixes up unambiguous
	// file-language cases; fails loudly on ambiguous ones.
	ix.enforceMethodLangInvariant()
	// Resolve constructor-inferred callee qualifiers now that the full
	// package symbol set is merged (per-file extraction cannot see
	// cross-file constructors): "New.M" -> "Index.M" and
	// "Server.newGov.M" -> "Gov.M". Conservative — single-return
	// constructors whose return type is an actual declared type only.
	ix.rewriteConstructorCallees()
	ix.Callers = map[string][]string{}
	ix.AliasCallers = map[string][]string{}
	// Project package directory bases (e.g. "lock", "memory") vs foreign
	// import bases ("fmt", "time"): the receiver-qualified merge below must
	// never attribute a foreign package's callee ("fmt.Println") to a local
	// symbol, even when the simple name is unique project-wide.
	foreignImportBases := ix.foreignImportBases()
	// Names the project itself defines — types, receivers and constructors —
	// used to tell a receiver-qualified single-dot key ("Client.Code") from an
	// unresolvable-variable key ("mystery.M") whose receiver is defined
	// nowhere: the latter must never merge onto a local symbol's canonical
	// bucket (CallersIncludingAliases still finds it via the alias layer).
	definedQualifierNames := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Receiver != "" {
			definedQualifierNames[s.Receiver] = true
		}
		switch s.Kind {
		case "type", "struct", "interface", "class", "record", "enum", "func", "method", "function":
			definedQualifierNames[s.Name] = true
		}
	}
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			if ce.Dynamic {
				continue
			}
			c := ce.Target
			if c == caller {
				continue
			}
			// Canonical edge: recorded under the exact callee key.
			ix.Callers[c] = append(ix.Callers[c], caller)
			// Merge package-qualified local calls ("db.Open") onto the local
			// symbol when the qualifier names its package dir; foreign and
			// unresolved targets never merge (avoids forging callers). The
			// merge is restricted to buckets that cannot mis-attribute: a
			// UNIQUE bare name owns its bare bucket outright (CallersFor
			// returns it without package filtering — the graph resolves any
			// qualifier for a unique simple name, so these callers must land
			// here or impact over-reports), while a SHARED bare name merges
			// only SAME-PACKAGE qualified callers — the bare-bucket
			// attribution (CallersFor) checks only the CALLER's package, so
			// a cross-package qualified caller ("learning.New" from
			// internal/app) would otherwise be mis-attributed to a
			// same-named symbol in the caller's package (app.New) even
			// though the recorded callee is learning.New — the caller
			// already sits under the canonical "learning.New" key, which
			// CallersFor's qualified-key loop attributes to the correct
			// symbol.
			if d, ok := qualifiedCalleeSymbol(ix, c); ok && d.FullName() != c && d.FullName() != caller {
				if len(ix.symbolsFor(d.FullName())) == 1 || callerInPackage(ix, caller, filepath.Dir(d.File)) {
					ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
				}
			}
			// Merge receiver/type/variable-qualified callee keys
			// ("Client.Code", "s.govSnapshot.Access.Authorize",
			// "NewGovernance.Access.CanRead") onto the unique same-named symbol
			// when the simple name identifies exactly one definition
			// project-wide. The graph's raw-edge collector resolves any qualifier
			// for a unique simple name (resolveNodeID case 1), so explore's
			// caller buckets must report those callers too or impact
			// over-reports. Keys whose qualifier is a foreign package import
			// base ("fmt.Println", "time.Date.Add") never merge: the callee is
			// not a local symbol, and a local same-named definition is never the
			// recorded target. A merge that would add the caller to its OWN
			// bucket (a stdlib chain rewritten onto the caller's own type, e.g.
			// Duration.String -> time.Duration.String landing on
			// "Duration.String") is a self-edge, never a caller — skipped.
			if d, ok := uniqueReceiverQualifiedSymbol(ix, c, foreignImportBases, definedQualifierNames); ok && d.FullName() != c && d.FullName() != caller {
				ix.Callers[d.FullName()] = append(ix.Callers[d.FullName()], caller)
			}
			// Merge receiver-chain callee keys whose bare name is SHARED
			// across receivers ("NewFirewall.WithAgents.AuditLog.All",
			// "f.AuditLog.All", "e.Confidence.String"): the qualifier's LAST
			// segment names the receiver type, mirroring the graph's
			// receiver match (resolveNodeID's booleanNested / exact-receiver
			// cases), which attributes such edges even when the simple name
			// is ambiguous. The merge only fires when the receiver+name
			// combination is UNIQUE project-wide: the method FullName bucket
			// ("Store.Save") is shared by every same-named method and
			// CallersFor returns it wholesale, so a chained caller would
			// otherwise be mis-attributed to all of them (the graph
			// attributes a chained receiver key to its first same-receiver
			// match only). Package-qualified keys ("learning.New") never
			// match a receiver named "learning" and stay untouched — the
			// shared-bare-bucket protection above still holds.
			if i := strings.LastIndexByte(c, '.'); i > 0 && i+1 < len(c) {
				qual, bare := c[:i], c[i+1:]
				recv := qual
				if j := strings.LastIndexByte(qual, '.'); j >= 0 {
					recv = qual[j+1:]
				}
				if recv != "" {
					var match Symbol
					matches := 0
					for _, d := range ix.symbolsFor(bare) {
						if d.Receiver == recv && d.FullName() != c {
							match = d
							matches++
						}
					}
					if matches == 1 {
						if match.FullName() != caller {
							ix.Callers[match.FullName()] = append(ix.Callers[match.FullName()], caller)
						}
					}
				}
			}
			// Also record a dotted callee under its bare name so simple-name
			// lookups find foreign or unresolved targets. Aliases stay in a
			// separate map, never merged into a local symbol's callers, and a
			// caller is never aliased to itself.
			if simple := simpleKey(c); simple != c && simple != caller {
				ix.AliasCallers[simple] = append(ix.AliasCallers[simple], caller)
			}
		}
	}
	for k := range ix.Callers {
		ix.Callers[k] = dedupeSorted(ix.Callers[k])
	}
	for k := range ix.AliasCallers {
		ix.AliasCallers[k] = dedupeSorted(ix.AliasCallers[k])
	}
	// Resolve structural interface implementations: match concrete types to interface
	// definitions in the same package.
	ifaceDir := map[string]string{}
	concreteDir := map[string]string{}
	concreteMethods := map[string]map[string]bool{}
	for _, s := range ix.Symbols {
		dir := filepath.Dir(s.File)
		if s.Kind == "interface" {
			ifaceDir[s.Name] = dir
		} else if s.Kind == "struct" || s.Kind == "type" {
			concreteDir[s.Name] = dir
		}
		if s.Kind == "method" && s.Receiver != "" {
			if concreteMethods[s.Receiver] == nil {
				concreteMethods[s.Receiver] = map[string]bool{}
			}
			concreteMethods[s.Receiver][s.Name] = true
			if concreteDir[s.Receiver] == "" {
				concreteDir[s.Receiver] = dir
			}
		}
	}
	// Group by directory before pairing: the (interface × concrete) cross
	// product below only ever matches same-directory names, so grouping
	// collapses an O(interfaces × concretes) iteration — millions of
	// dir-compare skips per finalize on a large repo — to exactly the
	// same-dir pairs that can match. Output is identical: every pair the
	// old loop skipped differed in dir, and the appended edges are
	// sorted+deduped below regardless of append order.
	ifacesByDir := map[string][]string{}
	for iface, dir := range ifaceDir {
		ifacesByDir[dir] = append(ifacesByDir[dir], iface)
	}
	concretesByDir := map[string][]string{}
	for concrete, dir := range concreteDir {
		concretesByDir[dir] = append(concretesByDir[dir], concrete)
	}
	for dir, ifaces := range ifacesByDir {
		concretes, ok := concretesByDir[dir]
		if !ok {
			continue
		}
		for _, iface := range ifaces {
			for _, concrete := range concretes {
				if concrete == iface {
					continue
				}
				if _, isIface := ifaceDir[concrete]; isIface {
					continue
				}
				if len(concreteMethods[concrete]) > 0 {
					edge := "implements:" + iface
					already := false
					for _, e := range ix.Inherits[concrete] {
						if e == edge {
							already = true
							break
						}
					}
					if !already {
						if ix.Inherits == nil {
							ix.Inherits = map[string][]string{}
						}
						ix.Inherits[concrete] = append(ix.Inherits[concrete], edge)
					}
				}
			}
		}
	}

	// The implements: edges above were appended in ifaceDir/concreteDir
	// MAP-ITERATION order (nondeterministic), so the Inherits values carry the
	// same nondeterminism class the InheritedBy reverse map had — that map is
	// dedupeSorted below, but the forward values were never sorted (finding
	// A1). Sort + dedupe them in the same finalize pass so the persisted
	// index is byte-deterministic regardless of map iteration order.
	for k := range ix.Inherits {
		ix.Inherits[k] = dedupeSorted(ix.Inherits[k])
	}

	// Reverse inheritance map: base name (and bare name) -> subtypes.
	ix.InheritedBy = map[string][]string{}
	for subtype, taggedBases := range ix.Inherits {
		for _, tb := range taggedBases {
			base := strings.TrimPrefix(tb, "extends:")
			base = strings.TrimPrefix(base, "implements:")
			base = strings.TrimPrefix(base, "embeds:")
			ix.InheritedBy[base] = append(ix.InheritedBy[base], subtype)
		}
	}
	for k := range ix.InheritedBy {
		ix.InheritedBy[k] = dedupeSorted(ix.InheritedBy[k])
	}
}

// addDispatchEdges adds virtual call edges from a method call on an interface
// or abstract type to every concrete implementation of that method, so DI call
// sites reach the code that actually runs.
func (ix *Index) addDispatchEdges() {
	if len(ix.InheritedBy) == 0 {
		return
	}

	// Build a set of all symbol full names for quick membership checks.
	symSet := map[string]bool{}
	methodsByReceiver := map[string]map[string]bool{} // receiver -> set of method names
	for _, s := range ix.Symbols {
		fn := s.FullName()
		symSet[fn] = true
		if s.Receiver != "" {
			if methodsByReceiver[s.Receiver] == nil {
				methodsByReceiver[s.Receiver] = map[string]bool{}
			}
			methodsByReceiver[s.Receiver][s.Name] = true
		}
	}

	// For each interface/abstract type that has implementers, add virtual edges
	// to each implementer's method of the same name.
	added := map[string]bool{} // dedupe key "caller->virtualCallee"
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			if ce.Dynamic || ce.Synth == "dispatch" || declarationBinding(ce.Binding) {
				continue
			}
			c := ce.Target
			// Parse "Receiver.method" to find the receiver and method name.
			dot := strings.LastIndex(c, ".")
			if dot < 0 || dot == 0 {
				continue
			}
			receiver := c[:dot]
			method := c[dot+1:]
			if receiver == "" || method == "" {
				continue
			}

			implementers := ix.InheritedBy[receiver]
			if len(implementers) == 0 {
				continue
			}

			for _, impl := range implementers {
				virtualCallee := impl + "." + method
				if !symSet[virtualCallee] {
					continue
				}
				// A self virtual-dispatch edge (the caller is itself the
				// implementer) is noise: computeCallers skips self-edges, so
				// recording one here made the build-time Callers map differ
				// from the recomputed one on every load path (JSON↔SQLite
				// parity), for an edge no traversal wants.
				if virtualCallee == caller {
					continue
				}
				key := caller + "->" + virtualCallee + "\x00" + ce.SourceFile
				if added[key] {
					continue
				}
				added[key] = true
				// Virtual dispatch edges are inferred through the inheritance
				// graph, not stated in source: LOW.
				ix.Calls[caller] = append(ix.Calls[caller], CallEdge{Target: virtualCallee, Confidence: ConfidenceLow, SourceFile: ce.SourceFile, Synth: "dispatch"})
				ix.Callers[virtualCallee] = append(ix.Callers[virtualCallee], caller)
			}
		}
	}

	// Dedupe after adding virtual edges.
	for k := range ix.Calls {
		ix.Calls[k] = langparse.DedupeCallEdges(ix.Calls[k])
	}
	for k := range ix.Callers {
		ix.Callers[k] = dedupeSorted(ix.Callers[k])
	}
}

func dedupeSorted(in []string) []string {
	if in == nil {
		return nil
	}
	if len(in) == 0 {
		return in
	}
	cp := append([]string(nil), in...)
	slices.Sort(cp)
	return slices.Compact(cp)
}

// CallersOf returns the functions that call a given symbol name.
func (ix *Index) CallersOf(symbol string) []string {
	return ix.Callers[symbol]
}

// CallSites returns the call edges of a symbol (what it calls).
func (ix *Index) CallSites(symbol string) []string {
	return CallEdgeTargets(ix.Calls[symbol])
}

// simpleKey returns the part of a recorded callee key after the last '.'
// ("" for a plain name).
func simpleKey(c string) string {
	if i := strings.LastIndexByte(c, '.'); i >= 0 {
		return c[i+1:]
	}
	return c
}

// qualifiedCalleeSymbol resolves a dotted callee key ("db.Open") to the local
// symbol whose package directory matches the qualifier. It returns ok=false
// for foreign targets ("fmt.Println") and unresolved receiver calls ("v.M"),
// whose callers must never be attributed to a local symbol of the same name.
// A package-qualified reference names a package-level symbol: when the
// package defines the name receiver-less, that func is the target — a
// same-named METHOD in the same package must not win ("tokenize.Count" is the
// func Count's form; BPECounter.Count is receiver-qualified and bpe.go sorts
// before tokenize.go, so the first same-base match used to pool the func's
// callers onto the method).
func qualifiedCalleeSymbol(ix *Index, c string) (Symbol, bool) {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		return Symbol{}, false
	}
	qualifier, bare := c[:i], c[i+1:]
	var methodFallback Symbol
	haveMethodFallback := false
	for _, d := range ix.symbolsFor(bare) {
		if filepath.Base(filepath.Dir(d.File)) != qualifier {
			continue
		}
		if d.Receiver == "" {
			return d, true
		}
		if !haveMethodFallback {
			methodFallback, haveMethodFallback = d, true
		}
	}
	return methodFallback, haveMethodFallback
}

// uniqueReceiverQualifiedSymbol returns the single project symbol whose
// simple name is the final segment of a receiver/type/variable-qualified
// callee key ("Client.Code", "s.Access.Authorize"), when that simple name
// identifies exactly one definition project-wide. The graph resolves any
// qualifier for a unique simple name, so these callers must land in the
// explore bucket of that one symbol. A qualifier chain (≥2 dots) is never a
// package-qualified reference in Go source (a package selector cannot carry
// further selectors), so chains resolve whenever the simple name is unique
// and the chain does not start at a foreign package import base
// ("time.Now.UTC.Add"). A single-dot key resolves only when its qualifier
// is a name the project itself defines — a type, receiver or constructor
// ("Client.Code") — never a foreign package ("fmt.Println",
// "json.Unmarshal") or an unresolvable variable ("mystery.M"): those callers
// stay in the canonical bucket under their own key and in the alias layer,
// never merged into a local symbol's canonical bucket (the
// TestCallersIncludingAliases contract).
func uniqueReceiverQualifiedSymbol(ix *Index, c string, foreignImportBases, definedQualifierNames map[string]bool) (Symbol, bool) {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		return Symbol{}, false
	}
	matches := ix.symbolsFor(c[i+1:])
	if len(matches) != 1 {
		return Symbol{}, false
	}
	first := c[:strings.IndexByte(c, '.')]
	if foreignImportBases[first] {
		return Symbol{}, false
	}
	if !strings.Contains(c[strings.IndexByte(c, '.')+1:], ".") && !definedQualifierNames[first] {
		// Single-dot key whose qualifier is not a project-defined
		// name: a foreign package or an unresolvable variable — never
		// a local symbol's receiver. The canonical map must stay
		// resolved-only; CallersFor's unique-name path still surfaces
		// these callers (the graph resolves any qualifier for a unique
		// name), so parity holds without polluting the canonical map.
		return Symbol{}, false
	}
	return matches[0], true
}

// CallersFor returns deduplicated, package-aware callers attributable to a
// local symbol. Methods key under the unique "Type.Method" full name; an
// unambiguous bare name owns its bucket outright — both keep exact recorded
// callers. When packages share a simple name, a bare edge is attributed to s
// only when the caller resolves to s's own package directory, and qualified
// keys attribute only to the exact symbol they resolve to. A cross-package
// bare caller is never attributed to a same-named symbol.
func (ix *Index) callersForLegacy(s Symbol) []string {
	exact := ix.Callers[s.FullName()]
	if s.Receiver != "" || len(ix.symbolsFor(s.Name)) == 1 || s.File == "" {
		out := dedupeSorted(exact)
		// Unique simple name: the graph resolves ANY qualifier for a unique
		// name (resolveNodeID case 1), so every dotted callee key ending in
		// the name attributes its callers to the one symbol — except keys
		// whose first qualifier is a foreign import base ("fmt.Println");
		// this only widens the RENDERED caller set.
		if len(ix.symbolsFor(s.Name)) == 1 {
			foreign := ix.callerIndex().foreignBases
			for k, callers := range ix.callerIndex().bySimpleKey[s.Name] {
				if k == s.FullName() {
					continue
				}
				i := strings.IndexByte(k, '.')
				if i <= 0 || foreign[k[:i]] {
					continue
				}
				for _, c := range callers {
					// A caller that IS this symbol is its own chained
					// self-edge — never a caller (computeCallers skips
					// self-edges; so must this widened render).
					if c == s.FullName() {
						continue
					}
					out = append(out, c)
				}
			}
		}
		// A METHOD's bare-name bucket is not its own: the graph's
		// caller-package fallback resolves a same-package bare reference to
		// the method when it is the package's only definition of the name —
		// mirror that (bare FUNCS own theirs via exact above).
		if s.Receiver != "" {
			dir := filepath.Dir(s.File)
			// Package-qualified keys ("metrics.Load") whose qualifier names
			// the package: the graph resolves a RESOLVABLE caller's
			// qualifier through its own imports and falls back to a
			// last-segment heuristic ONLY for unresolvable callers. Mirror
			// that: only the key's UNRESOLVABLE callers join — attributing
			// a resolvable caller by directory base would over-attribute
			// (a directory base like "metrics" is not unique to one
			// package). A qualified key naming a package's RECEIVER-LESS symbol
			// belongs to that func, never to a same-named method.
			hasLocalFunc := false
			for _, d := range ix.symbolsFor(s.Name) {
				if d.Receiver == "" && filepath.Dir(d.File) == dir {
					hasLocalFunc = true
					break
				}
			}
			if !hasLocalFunc {
				for k, callers := range ix.callerIndex().bySimpleKey[s.Name] {
					if k == s.FullName() {
						continue
					}
					i := strings.LastIndexByte(k, '.')
					if i <= 0 || i+1 >= len(k) {
						continue
					}
					if k[:i] != filepath.Base(dir) {
						continue
					}
					for _, c := range callers {
						if len(ix.symbolsFor(c)) != 1 {
							out = append(out, c)
						}
					}
				}
			}
			// Bare-name bucket: a method named with a Go predeclared
			// identifier is never bare-callable (a bare reference binds to
			// the builtin or a shadowing func), so that bucket must not
			// widen the method's callers.
			if !isPredeclared(s.Name) && exactlyOneLocalDef(ix, s.Name, dir) {
				for _, c := range ix.Callers[s.Name] {
					// Mirror the graph's callee-side resolution of a bare
					// callee, which depends on the CALLER's form: a
					// receiver-qualified caller is resolved by trying every
					// receiver-matched candidate — the same-package def
					// wins (callerHasLocalDef); a bare caller needs every
					// def local (callerInPackage — an ambiguous name must
					// not leak across packages); either form also joins when
					// the caller's package imports this def's package.
					if ix.callerAttributedBare(c, s, dir) {
						out = append(out, c)
					}
				}
			}
		}
		return dedupeSorted(out)
	}
	dir := filepath.Dir(s.File)
	var out []string
	// Package-qualified keys ("db.Open", "lock.Acquire") whose qualifier names
	// a directory holding a definition of s: the bucket is attributed to every
	// same-directory candidate — two packages can share a directory base
	// ("memory") and build-tagged duplicates can share one package
	// (lock_unix.go/lock_windows.go). A RESOLVABLE caller (unique simple
	// name) is attributed only when its package actually imports this
	// definition's package under the qualifier — the graph resolves the
	// qualified callee through the CALLER's imports (a "governance.X" call
	// must not base-match the MCP wrapper in internal/mcp/governance).
	// Unresolvable callers join via the last-segment package fallback.
	for k, callers := range ix.callerIndex().bySimpleKey[s.Name] {
		if k == s.FullName() {
			continue
		}
		i := strings.LastIndexByte(k, '.')
		if i <= 0 || i+1 >= len(k) {
			continue
		}
		qual := k[:i]
		for _, d := range ix.symbolsFor(s.Name) {
			if filepath.Base(filepath.Dir(d.File)) == qual && filepath.Dir(d.File) == dir {
				for _, c := range callers {
					if len(ix.symbolsFor(c)) != 1 || ix.callerImportsDef(c, d, qual) {
						out = append(out, c)
					}
				}
				break
			}
		}
	}
	// Bare-name bucket: same-package callers only, gated by exactlyOneLocalDef
	// (methods included). Two graph paths are mirrored: (a) a UNIQUE
	// receiver-less definition project-wide owns every bare edge — an
	// ambiguous caller still joins when it has a definition in the package
	// (same-package preference); (b) otherwise a shared bare name needs a
	// caller wholly in the package. The strict callerInPackage stays in force
	// for the computeCallers merge guard (no cross-package qualified caller
	// may enter a shared bare bucket).
	for _, c := range exact {
		if uniqueReceiverLessDef(ix, s.Name) {
			// Path (a): the bare name resolves to the unique receiver-less
			// definition regardless of local methods or the caller's
			// ambiguity; the caller joins via same-package preference.
			if callerHasLocalDef(ix, c, dir) {
				out = append(out, c)
			}
		} else if exactlyOneLocalDef(ix, s.Name, dir) && ix.callerAttributedBare(c, s, dir) {
			// Path (b): shared bare name — the graph's caller-package
			// fallback needs an unambiguous local resolution (methods
			// included) and a caller resolved into the package (any local
			// def wins for a receiver-qualified caller; every def local
			// for a bare caller; or an import-linked caller).
			out = append(out, c)
		}
	}
	return dedupeSorted(out)
}

// uniqueReceiverLessDef reports whether name has exactly ONE receiver-less
// definition project-wide — the graph's resolveNodeID qualified-match: a
// bare reference resolves to the unique receiver-less definition (a method's
// Qualified is receiver-qualified and never equals the bare reference). When
// this holds, every bare edge on the name calls that one definition, so the
// caller-side attribution can use the graph's same-package preference even
// for an ambiguous caller name.
func uniqueReceiverLessDef(ix *Index, name string) bool {
	n := 0
	for _, d := range ix.symbolsFor(name) {
		if d.Receiver == "" {
			n++
		}
	}
	return n == 1
}

// exactlyOneLocalDef reports whether the package at dir defines the bare
// name exactly once — methods included. A single local definition owns its
// bare edges; two or more (a func and a same-named method, or build-tagged
// duplicates like Acquire in lock_unix.go/lock_windows.go) leave a bare
// reference ambiguous within the package and attribute nothing — the same
// ambiguity the graph's caller-package fallback (resolveEndpointPackageAware)
// hits when several same-named local candidates block the resolution. (The
// bare-bucket loop still attributes the caller when the name has a UNIQUE
// receiver-less definition project-wide — the graph's resolveNodeID
// qualified-match path, which ignores methods entirely.)
func exactlyOneLocalDef(ix *Index, name, dir string) bool {
	n := 0
	for _, d := range ix.symbolsFor(name) {
		if filepath.Dir(d.File) == dir {
			n++
		}
	}
	return n == 1
}

// foreignImportBases returns the set of foreign package import bases ("fmt",
// "time") — import path bases that are not project package directory bases —
// used to keep a foreign qualified callee ("fmt.Println") from ever
// attributing its callers to a local same-named symbol. Computed per call
// (the maps are small); deliberately not persisted on the index.
func (ix *Index) foreignImportBases() map[string]bool {
	pkgDirBases := map[string]bool{}
	for path := range ix.Pkgs {
		if base := filepath.Base(path); base != "" {
			pkgDirBases[base] = true
		}
	}
	out := map[string]bool{}
	for _, p := range ix.Pkgs {
		for _, imp := range p.Imports {
			if alias := strings.TrimSpace(imp.Alias); alias != "" && alias != "_" && alias != "." {
				out[alias] = true
			}
			base := filepath.Base(imp.Path)
			if base == "" || pkgDirBases[base] {
				continue
			}
			out[base] = true
			// gopkg.in/yaml.v3 is imported as package yaml; the version
			// suffix is not a Go identifier, so also record the stripped
			// base or the qualifier never matches a foreign import.
			if i := strings.IndexByte(base, '.'); i > 0 {
				out[base[:i]] = true
			}
		}
	}
	return out
}

// callerInPackage reports whether a caller endpoint resolves to a symbol whose
// file lives in dir. An unresolvable or multi-package caller name is never
// attributed: a bare edge whose caller could be a different package's
// same-named symbol must not merge into this symbol's callers.
func callerInPackage(ix *Index, caller, dir string) bool {
	matches := ix.symbolsFor(caller)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if filepath.Dir(m.File) != dir {
			return false
		}
	}
	return true
}

// callerHasLocalDef reports whether a caller endpoint has AT LEAST ONE
// definition in dir — the graph's same-package preference for an ambiguous
// caller name (resolveEndpointPackageAware attributes the caller to the
// same-package candidate when several packages share the name). Used by
// CallersFor's bare-bucket attribution; the strict all-matches
// callerInPackage remains the gate for the computeCallers merge guard so a
// cross-package qualified caller can never enter a shared bare bucket.
func callerHasLocalDef(ix *Index, caller, dir string) bool {
	for _, m := range ix.symbolsFor(caller) {
		if filepath.Dir(m.File) == dir {
			return true
		}
	}
	return false
}

// filePkgIndex maps a source file to its package path via the Pkgs file
// lists. Cheap (O(pkgs x files)); used only in the rendered-caller widening
// paths, never per-edge in a hot loop.
