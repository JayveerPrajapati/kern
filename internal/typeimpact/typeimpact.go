package typeimpact

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/index"
	"path"
	"strings"
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

// IsTypeKind reports whether a symbol kind names a type declaration. The Go
// extractor uses "struct"/"interface"; other parsers emit "class"/"type"/
// "enum"/"record".
func IsTypeKind(kind string) bool {
	switch kind {
	case "struct", "interface", "class", "type", "enum", "record":
		return true
	}
	return false
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
