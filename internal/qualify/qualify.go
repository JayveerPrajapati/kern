// Package qualify resolves a package-qualified symbol query to the definition
// whose file path carries that qualifier.
//
// Two tiers, tightest first: the trailing directory segments (Go package dirs
// are unique), then an ANCESTOR segment run (workspaces where every crate's
// sources live under src/ — Rust/Java — so the crate name is the only usable
// qualifier). A tier with more than one candidate is refused, so an inexact
// qualifier can never select the wrong definition.
package qualify

import (
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// NonProduction reports whether a symbol is a test or fixture definition. The
// caller supplies it so this package stays free of explore's test heuristics.
type NonProduction func(index.Symbol) bool

// Definition honors a package qualifier ("verifycmd.checkRule",
// "internal/verifycmd.checkRule", "densic-query.resolve") when the picked
// definition lives elsewhere. Type.Method queries (qualifier equals the
// receiver) and unqualified names never re-point.
func Definition(ix *index.Index, symbol string, picked index.Symbol, nonProd NonProduction) (index.Symbol, bool) {
	i := strings.LastIndexByte(symbol, '.')
	if i <= 0 || i+1 >= len(symbol) {
		return index.Symbol{}, false
	}
	qual, name := symbol[:i], symbol[i+1:]
	if picked.Name != name || picked.Receiver == qual ||
		PathSuffix(picked.File, qual) || PathContains(picked.File, qual) {
		return index.Symbol{}, false
	}
	return uniqueNamed(ix, qual, name, nonProd)
}

// How reports how a query resolved to d, for the "resolved X -> Y (how)" note.
// Only a qualifier that actually selected this definition earns
// "package-qualified match"; one that matched nothing keeps the honest
// "fuzzy match" label instead of claiming a selection it did not make.
func How(ix *index.Index, query string, d index.Symbol, nonProd NonProduction) string {
	i := strings.LastIndexByte(query, '.')
	if i <= 0 || i+1 >= len(query) {
		return "fuzzy match"
	}
	qual, name := query[:i], query[i+1:]
	if name != d.Name || d.Receiver == qual {
		return "fuzzy match"
	}
	if s, ok := uniqueNamed(ix, qual, name, nonProd); ok &&
		s.File == d.File && s.Line == d.Line && s.Kind == d.Kind {
		return "package-qualified match"
	}
	return "fuzzy match"
}

// uniqueNamed returns the single non-test, same-named symbol whose path carries
// qual. Methods are eligible: a workspace crate's implementation often IS a
// method ("SymbolIndex.resolve" in crates/x/src/), and the qualifier here is a
// path, not a type. A type qualifier ("Paths.resolve") cannot match by path, so
// it keeps its Type.Method behaviour.
func uniqueNamed(ix *index.Index, qual, name string, nonProd NonProduction) (index.Symbol, bool) {
	if ix == nil {
		return index.Symbol{}, false
	}
	for _, match := range []func(string, string) bool{PathSuffix, PathContains} {
		var found index.Symbol
		n := 0
		for _, s := range ix.Symbols {
			if s.Name != name || !isDefinitionKind(s.Kind) || !match(s.File, qual) ||
				(nonProd != nil && nonProd(s)) {
				continue
			}
			found = s
			n++
			if n > 1 {
				break
			}
		}
		if n == 1 {
			return found, true
		}
		if n > 1 {
			return index.Symbol{}, false
		}
	}
	return index.Symbol{}, false
}

// isDefinitionKind reports whether kind names a definition a package qualifier
// may re-point to. A Rust `mod resolve;` in the crate root shares the name of
// the real item but is only a DECLARATION — counting it would make every crate
// qualifier look ambiguous. Re-exports and headings are likewise not targets.
func isDefinitionKind(kind string) bool {
	switch kind {
	case "module", "import", "heading", "entry":
		return false
	}
	return true
}

// UnboundCallers counts the call sites the index recorded under sym's BARE
// name that no qualifier can attribute to one declaration, because that name is
// declared in more than one file. For such a symbol an empty caller list is
// missing coverage, not an all-clear: the index has the edges, it just cannot
// say which declaration they enter.
func UnboundCallers(ix *index.Index, sym index.Symbol) int {
	if ix == nil || sym.Name == "" {
		return 0
	}
	files := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Name == sym.Name && isDefinitionKind(s.Kind) {
			files[s.File] = true
		}
	}
	if len(files) < 2 {
		return 0
	}
	seen := map[string]bool{}
	for _, caller := range ix.Callers[sym.Name] {
		seen[caller] = true
	}
	return len(seen)
}

// PathSuffix reports whether the directory part of file ends with the
// '/'-separated segments of prefix ("internal/mcp/context/server.go" carries
// the prefix "mcp/context"). An empty prefix matches every file.
func PathSuffix(file, prefix string) bool {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return true
	}
	segs := strings.Split(prefix, "/")
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

// PathContains reports whether the '/'-separated qualifier segments appear as a
// contiguous run of DIRECTORY segments anywhere in file's path — an ancestor
// match ("crates/densic-query" for ".../crates/densic-query/src/resolve.rs").
// The file name is never part of the match, and an empty qualifier matches
// nothing (unlike PathSuffix, whose empty prefix is a deliberate wildcard for
// other callers).
func PathContains(file, prefix string) bool {
	segs := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}
	fsegs := strings.Split(strings.Trim(filepath.Dir(file), "/"), "/")
	for start := 0; start+len(segs) <= len(fsegs); start++ {
		ok := true
		for i, s := range segs {
			if fsegs[start+i] != s {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
