package typeimpact

import (
	"fmt"
	"path"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/typealias"
)

// TypeImpactRoots expands a TYPE declaration into the symbols that actually
// carry call edges. Go/Java/TypeScript call graphs attach edges to METHODS and
// constructors, never to the type node itself, so `kern impact FileStore`
// reported "0 callers, risk low" for a persistence core used across 20+ files
// — a confident false all-clear on the product's flagship query.
//
// The expansion is deliberately conservative: methods whose Receiver is the
// type, plus functions in the SAME DIRECTORY that either return the type
// (indexed Returns) or follow the New<Type> naming convention. A matching
// receiver name in another directory is excluded so two packages with the
// same type name never inflate each other's blast radius.
//
// expansion is a human sentence for the report ("FileStore → 12 methods, 3
// constructors"); empty when the type has no methods or constructors.
func TypeImpactRoots(d *index.Symbol, symbols []index.Symbol) (roots []string, expansion string, ok bool) {
	if d == nil || !IsTypeKind(d.Kind) {
		return nil, "", false
	}
	dir := path.Dir(strings.ReplaceAll(d.File, "\\", "/"))
	roots = []string{d.FullName()}
	seen := map[string]bool{d.FullName(): true}
	methods, ctors := 0, 0
	add := func(full string) bool {
		if full == "" || seen[full] {
			return false
		}
		seen[full] = true
		roots = append(roots, full)
		return true
	}
	for i := range symbols {
		s := symbols[i]
		if path.Dir(strings.ReplaceAll(s.File, "\\", "/")) != dir {
			continue
		}
		switch {
		case s.Kind == "method" && s.Receiver == d.Name:
			if add(s.FullName()) {
				methods++
			}
		case s.Kind == "func" && (ReturnsType(s.Returns, d.Name) ||
			s.Name == "New"+d.Name || strings.HasPrefix(s.Name, "New"+d.Name+"With")):
			if add(s.FullName()) {
				ctors++
			}
		}
	}
	if methods+ctors == 0 {
		return roots, "", true
	}
	return roots, fmt.Sprintf("%s → %d methods, %d constructors", d.Name, methods, ctors), true
}

// TypeImpactRootsWithIndex is TypeImpactRoots plus alias awareness: when d is
// a Go type alias (`type A = B`), the aliased declaration's methods and
// constructors are roots too, so a query on the alias cannot report the
// aliased type's blast radius as "0 methods, low risk". The expansion names
// the alias so the report is never ambiguous.
func TypeImpactRootsWithIndex(ix *index.Index, d *index.Symbol) (roots []string, expansion string, ok bool) {
	roots, expansion, ok = TypeImpactRoots(d, ix.Symbols)
	if !ok {
		return nil, "", false
	}
	tgt, has := typealias.Target(ix, d)
	if !has {
		return roots, expansion, true
	}
	troots, texp, _ := TypeImpactRoots(tgt, ix.Symbols)
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	for _, r := range troots {
		if !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	label := d.Name + " (alias of " + tgt.Name + ")"
	if pkg := path.Base(path.Dir(strings.ReplaceAll(tgt.File, "\\", "/"))); pkg != "" && pkg != "." && pkg != "-" && pkg != d.Name {
		label = d.Name + " (alias of " + pkg + "." + tgt.Name + ")"
	}
	if texp == "" {
		return roots, label, true
	}
	if i := strings.Index(texp, " → "); i >= 0 {
		return roots, label + texp[i:], true
	}
	return roots, label + " → " + texp, true
}

// AliasTarget is the alias target of d, or nil when d is not an alias.
func AliasTarget(ix *index.Index, d *index.Symbol) *index.Symbol {
	if t, ok := typealias.Target(ix, d); ok {
		return t
	}
	return nil
}

// AliasesOf lists every indexed declaration that aliases d.
func AliasesOf(ix *index.Index, d *index.Symbol) []index.Symbol {
	return typealias.AliasesOf(ix, d)
}

// IsTypeKind reports whether a symbol kind names a type declaration. The Go
// extractor uses "struct"/"interface"; other parsers emit "class"/"type"/
// "enum"/"record".
func IsTypeKind(kind string) bool {
	return typealias.IsTypeKind(kind)
}

// ReturnsType reports whether any declared return type names typ. Pointers,
// slices, maps, channels, generics and package qualifiers are stripped first
// ("*FileStore", "pkg.FileStore", "[]*FileStore" → "FileStore").
func ReturnsType(returns []string, typ string) bool {
	for _, r := range returns {
		if normalizeTypeName(r) == typ {
			return true
		}
	}
	return false
}

func normalizeTypeName(s string) string {
	s = strings.TrimSpace(s)
	// Strip generic arguments: Store[T] -> Store.
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	// Drop pointer/slice/map/channel decoration and surrounding punctuation.
	s = strings.TrimLeft(s, "*[](){} \t")
	if i := strings.LastIndexAny(s, "*. "); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}
