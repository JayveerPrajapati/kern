package index

import (
	"path/filepath"
	"slices"
	"strings"
)

func (ix *Index) CallersOfName(name string) []string {
	if len(ix.symbolsFor(name)) > 0 {
		return dedupeSorted(ix.Callers[name])
	}
	if exact := ix.Callers[name]; len(exact) > 0 {
		return dedupeSorted(exact)
	}
	return dedupeSorted(ix.AliasCallers[name])
}

// CallsFor returns deduplicated callees recorded under the exact key of s,
// selecting edges from s's source file when provenance is available. Synthetic
// and legacy edges without a source file retain import/package attribution.
func (ix *Index) CallsFor(s Symbol) []string {
	edges := ix.Calls[s.FullName()]
	owned := false
	for _, edge := range edges {
		if edge.SourceFile != "" {
			owned = true
			break
		}
	}
	if owned && s.File != "" {
		var targets []string
		for _, edge := range edges {
			if edge.SourceFile == s.File || (edge.SourceFile == "" && calleeAttributable(ix, edge.Target, s)) {
				targets = append(targets, edge.Target)
			}
		}
		return dedupeSorted(targets)
	}
	if s.Receiver != "" || len(ix.symbolsFor(s.Name)) == 1 || s.File == "" {
		return dedupeSorted(CallEdgeTargets(edges))
	}
	var out []string
	for _, e := range edges {
		if calleeAttributable(ix, e.Target, s) {
			out = append(out, e.Target)
		}
	}
	return dedupeSorted(out)
}

// calleeAttributable reports whether a recorded callee target belongs to s.
func calleeAttributable(ix *Index, c string, s Symbol) bool {
	i := strings.LastIndexByte(c, '.')
	if i <= 0 || i+1 >= len(c) {
		// Bare callee: keep when it resolves into s's package, or when it is
		// foreign — builtins and stdlib — which s's source can reference
		// without a qualifier. A bare name with no same-package declaration
		// resolves to the predeclared identifier (Go scoping), so a local
		// symbol of the same name in an unrelated package is never the
		// recorded callee.
		matches := ix.symbolsFor(c)
		if len(matches) == 0 {
			return true
		}
		dir := filepath.Dir(s.File)
		for _, m := range matches {
			if filepath.Dir(m.File) == dir {
				return true
			}
		}
		return isPredeclared(c)
	}
	qualifier := c[:i]
	// Package-qualified call from s's own file: the qualifier must name one
	// of s's imports ("fmt.Errorf", "domain.X"). A qualifier matching no
	// import of s's file was recorded from a different package's file (the
	// same-named twin), never from s.
	if ix.ImportsByFile != nil {
		for _, imp := range ix.ImportsByFile[s.File] {
			if filepath.Base(imp.Path) == qualifier {
				return true
			}
		}
	}
	// Receiver-qualified call on a type declared in s's package
	// ("Hooks.LoadIndex" from the wrapper, "Firewall.Check" from the core).
	if q0 := qualifier[0]; q0 >= 'A' && q0 <= 'Z' {
		dir := filepath.Dir(s.File)
		for _, t := range ix.symbolsFor(qualifier) {
			if filepath.Dir(t.File) == dir {
				return true
			}
		}
		return false
	}
	// Lowercase qualifier naming s's own package dir: a same-package call
	// recorded with a qualifier can only come from another file that imports
	// s's package — never from s itself — so it is the twin's edge.
	if d, ok := qualifiedCalleeSymbol(ix, c); ok && filepath.Dir(d.File) == filepath.Dir(s.File) {
		return false
	}
	return false
}

// isPredeclared reports whether name is a Go predeclared identifier (builtin
// function, type, constant or nil). A bare callee with that name and no
// same-package declaration is the builtin, never an unrelated package's
// same-named symbol (cross-package references always carry a qualifier).
func isPredeclared(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete",
		"imag", "len", "make", "max", "min", "new", "panic", "print",
		"println", "real", "recover",
		"bool", "byte", "complex64", "complex128", "error", "float32",
		"float64", "int", "int8", "int16", "int32", "int64", "rune",
		"string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"true", "false", "iota", "nil":
		return true
	}
	return false
}

// edgeKeys returns the map keys under which a symbol's inheritance edges may
// be recorded: the bare name and, for methods, the "Type.Method" form.
// Inheritance keys only ever store type names (FullName == Name), so both
// forms coincide.
func edgeKeys(s Symbol) []string {
	if fn := s.FullName(); fn != s.Name {
		return []string{s.Name, fn}
	}
	return []string{s.Name}
}

// rewriteConstructorCallees rewrites callee qualifiers that are constructor
// names into the constructor's single return type. Two shapes:
//
//	"New.applyFileResult"   -> "Index.applyFileResult"   (func constructor)
//	"s.newGov.Filter"       -> "Gov.Filter"              (method constructor
//	                                                      via a multi-value
//	                                                      receiver-var assign)
//
// The rewrite is conservative: it fires only when the constructor's FIRST
// return names an actual type symbol declared in the project (Go convention
// puts the error last, so "gov, err := ..." maps through Returns[0]).
// Unambiguous method names only (a duplicated method name skips the rewrite).
// Runs before the Callers inversion in every build path (buildSerial,
// buildParallel, Update all call computeCallers).
func (ix *Index) rewriteConstructorCallees() {
	if len(ix.Symbols) == 0 {
		return
	}
	ctorRet := map[string]string{} // func name -> first return type
	ctorDup := map[string]bool{}   // shared bare constructor names skip the rewrite
	methodRet := map[string]string{}
	methodDup := map[string]bool{}
	types := map[string]bool{}
	for _, s := range ix.Symbols {
		switch s.Kind {
		case "func":
			// The FIRST return is the constructed value; Go convention puts
			// the error last ("gov, err := ..."). The types[] check at
			// rewrite time keeps this honest. A shared bare constructor name
			// (two packages both declaring "New") is marked ambiguous and
			// skips the rewrite — the same guard the methodRet map has
			// (finding A6): last-write-wins over ix.Symbols order attributed
			// a bare "New.M" to an arbitrary package's type.
			if len(s.Returns) >= 1 {
				if _, dup := ctorRet[s.Name]; dup {
					ctorDup[s.Name] = true
					continue
				}
				ctorRet[s.Name] = s.Returns[0]
			}
		case "method":
			if len(s.Returns) >= 1 {
				if _, dup := methodRet[s.Name]; dup {
					methodDup[s.Name] = true
					continue
				}
				methodRet[s.Name] = s.Returns[0]
			}
		case "type", "struct", "interface":
			types[s.Name] = true
		}
	}
	// Package-merged struct field types ("App.taskSvc" -> "TaskService"),
	// collected from every file of every package. Both the project-wide map
	// and the per-package maps merge in SORTED package-path order: a field
	// key shared by two packages ("Client.http" — internal/mcpclient's
	// *httpClient vs the Go SDK's *http.Client) previously resolved by
	// first-write-wins over random ix.Pkgs map iteration, which made the
	// final index nondeterministic and occasionally forged a self-edge
	// ("Client.http.roundTrip" rewritten against the wrong package's field
	// type became "Client.roundTrip").
	paths := make([]string, 0, len(ix.Pkgs))
	for p := range ix.Pkgs {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	fieldTypes := map[string]string{}
	pkgFieldTypes := map[string]map[string]string{}
	filePkg := map[string]string{}
	for _, path := range paths {
		p := ix.Pkgs[path]
		pkgFieldTypes[path] = p.StructFields
		for _, f := range p.Files {
			if _, dup := filePkg[f]; !dup {
				filePkg[f] = path
			}
		}
		for k, v := range p.StructFields {
			if _, dup := fieldTypes[k]; !dup {
				fieldTypes[k] = v
			}
		}
	}
	// Package-merged constructor return types ("api.NewHandlers" ->
	// "Handlers"), keyed by both the package name and the import-path base
	// so calls resolve whether the source alias matched the package name
	// or the directory. Sorted package iteration keeps the first-wins
	// resolution deterministic (same collision class as the field types).
	ctorQual := map[string]string{}
	for _, path := range paths {
		p := ix.Pkgs[path]
		for k, v := range p.Constructors {
			if _, dup := ctorQual[p.Name+"."+k]; !dup {
				ctorQual[p.Name+"."+k] = v
			}
			if base := filepath.Base(p.Path); base != p.Name && base != "" {
				if _, dup := ctorQual[base+"."+k]; !dup {
					ctorQual[base+"."+k] = v
				}
			}
		}
	}
	// ownerPkg resolves a caller/owner key to its package so the
	// receiver-field chain rewrites against the OWNER's package's field map
	// first: the chain's first segment is the owner's receiver type
	// (resolveCallee at extract time resolved the receiver variable), so the
	// field belongs to that type in that package — the project-wide map must
	// not let a same-named field in another package win the rewrite.
	ownerPkg := func(owner string) string {
		defs := ix.symbolsFor(owner)
		if len(defs) != 1 {
			return ""
		}
		return filePkg[defs[0].File]
	}
	// fieldOf resolves a "Struct.field" prefix against the owner's package
	// first, then the deterministic sorted-merge project-wide map — the
	// fallback for chains whose struct lives in another package.
	fieldOf := func(owner, key string) (string, bool) {
		if pkg := ownerPkg(owner); pkg != "" {
			if pm := pkgFieldTypes[pkg]; pm != nil {
				if r, ok := pm[key]; ok && types[r] {
					return r, true
				}
			}
		}
		r, ok := fieldTypes[key]
		if !ok || !types[r] {
			return "", false
		}
		return r, true
	}
	rewrite := func(c, owner string) string {
		i := strings.LastIndexByte(c, '.')
		if i <= 0 || i == len(c)-1 {
			return c
		}
		q, m := c[:i], c[i+1:]
		// Func constructor: "New.M".
		if r, ok := ctorRet[q]; ok && !ctorDup[q] && types[r] {
			return r + "." + m
		}
		// Cross-package constructor-assigned receiver: "api.NewHandlers.Routes"
		// — the extractor resolved the receiver variable to the constructor's
		// qualified name (its return type is unknown outside the package), but
		// the package-merged Constructors map completes it here. Fires only
		// when the constructor's first return names a type declared in the
		// project (conservative, mirrors the other rewrites).
		if r, ok := ctorQual[q]; ok && types[r] {
			return r + "." + m
		}
		// Method constructor via receiver-var chain: "s.newGov.M" —
		// the last qualifier segment names the method.
		if j := strings.LastIndexByte(q, '.'); j >= 0 {
			qs := q[j+1:]
			if r, ok := methodRet[qs]; ok && !methodDup[qs] && types[r] {
				return r + "." + m
			}
		}
		// Receiver-field chain: "App.taskSvc.Deploy" — the extractor
		// resolved the receiver variable's type ("a" -> App) but the struct
		// may be declared in another file, so the field -> type link is only
		// visible here. Walk the longest matching "Struct.field" prefix and
		// rewrite to the field's type; iterate so multi-level chains
		// ("App.svc.client.M") resolve fully. Fires only when the field's
		// type is a type declared in the project (conservative, mirrors the
		// constructor rewrite).
		for {
			segs := strings.Split(c, ".")
			if len(segs) < 3 {
				break
			}
			rewritten := false
			for k := 1; k < len(segs)-1; k++ {
				key := strings.Join(segs[:k+1], ".")
				if r, ok := fieldOf(owner, key); ok {
					c = r + "." + strings.Join(segs[k+1:], ".")
					rewritten = true
					break
				}
			}
			if !rewritten {
				break
			}
		}
		return c
	}
	for caller, callees := range ix.Calls {
		for i, ce := range callees {
			ix.Calls[caller][i].Target = rewrite(ce.Target, caller)
		}
	}
}

// CallersIncludingAliases returns every recorded caller for a symbol's full
// name: the canonical callers plus simple-name aliases (edges the extractor
// recorded under a receiver-qualified or package-qualified callee form, e.g.
// "New.applyFileResult" when a constructor-inferred receiver variable is
// resolved to the constructor's name). Deletion/dead-code analysis must use
// this: MISSING a caller is the dangerous direction, and over-reporting only
// errs toward "unsafe to delete", never toward "safe".
func (ix *Index) CallersIncludingAliases(full string) []string {
	out := append([]string(nil), ix.Callers[full]...)
	if simple := simpleKey(full); simple != full {
		out = append(out, ix.AliasCallers[simple]...)
	} else {
		out = append(out, ix.AliasCallers[full]...)
	}
	return dedupeSorted(out)
}
