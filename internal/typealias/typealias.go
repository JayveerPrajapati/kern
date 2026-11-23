// Package typealias resolves Go type aliases (`type A = B`) against the
// index. Call edges and AST references attach to the ALIASED declaration, so
// a query on the alias name alone reports a confident all-clear (the audit
// case: `kern impact asttypes.Symbol` said 1 caller / 2 files while
// `index.Symbol` carried 843 dependents and 610 references). The alias RHS is
// recorded at index time (asttypes.Symbol.Alias), so resolution here is a
// pure index lookup — no file I/O or parsing on the query path. Indexes built
// before the Alias field simply carry no aliases and behave as they did
// before.
package typealias

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/astrefs"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/refscan"
)

// IsTypeKind reports whether a symbol kind names a type declaration.
func IsTypeKind(kind string) bool {
	switch kind {
	case "struct", "interface", "class", "type", "enum", "record":
		return true
	}
	return false
}

func slash(f string) string { return strings.ReplaceAll(f, "\\", "/") }

func dirOf(f string) string { return path.Dir(slash(f)) }

// packageName returns the indexed package name of a directory, falling back
// to the directory basename (the Go package name in practice).
func packageName(ix *index.Index, dir string) string {
	if ix != nil {
		if p, ok := ix.Pkgs[dir]; ok && p != nil && p.Name != "" {
			return p.Name
		}
	}
	return path.Base(dir)
}

// resolveRef resolves an alias RHS from its declaring symbol. A bare name must
// live in the SAME directory (two packages may declare the same type name); a
// qualified name must match the target package's name.
func resolveRef(ix *index.Index, from index.Symbol, ref string) *index.Symbol {
	ref = strings.TrimSpace(ref)
	if ix == nil || ref == "" {
		return nil
	}
	pkg, name := "", ref
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		pkg, name = ref[:i], ref[i+1:]
	}
	fromDir := dirOf(from.File)
	var fallback *index.Symbol
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Name != name || !IsTypeKind(s.Kind) {
			continue
		}
		dir := dirOf(s.File)
		if pkg == "" {
			if dir == fromDir {
				return s
			}
			continue
		}
		if dir == fromDir {
			continue
		}
		if packageName(ix, dir) == pkg {
			return s
		}
		if fallback == nil && path.Base(dir) == pkg {
			fallback = s
		}
	}
	return fallback
}

// maxChain bounds alias-of-alias chains (A = B, B = C) and cycle detection.
const maxChain = 4

// Target follows d's alias chain to the declaration that carries the methods
// and references. ok is false when d is not an alias or the target is missing
// from the index.
func Target(ix *index.Index, d *index.Symbol) (tgt *index.Symbol, ok bool) {
	if ix == nil || d == nil || strings.TrimSpace(d.Alias) == "" {
		return nil, false
	}
	seen := map[string]bool{}
	cur := d
	for i := 0; i < maxChain; i++ {
		if cur == nil || strings.TrimSpace(cur.Alias) == "" {
			break
		}
		key := slash(cur.File) + "\x00" + cur.Name
		if seen[key] {
			return nil, false
		}
		seen[key] = true
		if next := resolveRef(ix, *cur, cur.Alias); next != nil {
			cur = next
		} else {
			return nil, false
		}
	}
	if cur == nil || (cur.File == d.File && cur.Name == d.Name) {
		return nil, false
	}
	return cur, true
}

// AliasesOf returns every indexed declaration that aliases d (directly or
// through a chain) — the reverse direction, so a query on the TARGET also
// sees references written through the alias. Scans index symbols only; no I/O.
func AliasesOf(ix *index.Index, d *index.Symbol) []index.Symbol {
	if ix == nil || d == nil {
		return nil
	}
	var out []index.Symbol
	for i := range ix.Symbols {
		s := ix.Symbols[i]
		if strings.TrimSpace(s.Alias) == "" || (s.File == d.File && s.Name == d.Name) {
			continue
		}
		if t, ok := Target(ix, &s); ok && t.File == d.File && t.Name == d.Name {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Related returns d plus its alias counterpart(s): the target when d is an
// alias, every alias of d otherwise. d is always first.
func Related(ix *index.Index, d *index.Symbol) []index.Symbol {
	if d == nil {
		return nil
	}
	out := []index.Symbol{*d}
	if t, ok := Target(ix, d); ok {
		return append(out, *t)
	}
	return append(out, AliasesOf(ix, d)...)
}

// References unions the AST reference scans of every related name. A boundary
// reference (the alias declaration itself) mentions both names, so duplicates
// are dropped by position.
func References(ix *index.Index, d *index.Symbol) ([]astrefs.Reference, bool) {
	seen := map[string]bool{}
	var out []astrefs.Reference
	complete := true
	for _, s := range Related(ix, d) {
		refs, ok := astrefs.References(ix, s)
		if !ok {
			complete = false
		}
		for _, r := range refs {
			key := fmt.Sprintf("%s\x00%d\x00%d", r.File, r.Line, r.Col)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out, complete
}

// Lookup mirrors astrefs.Lookup over the alias-related names: AST references
// when any are found, else the bounded textual safety net.
func Lookup(ix *index.Index, d *index.Symbol) astrefs.Result {
	if refs, complete := References(ix, d); len(refs) > 0 {
		confidence := "ast"
		if !complete {
			confidence = "ast (scan truncated)"
		}
		return astrefs.Result{Files: astrefs.DistinctFiles(refs), Count: len(refs), Confidence: confidence}
	}
	seen := map[string]bool{}
	var files []string
	count := 0
	complete := true
	for _, s := range Related(ix, d) {
		f, n, ok := refscan.TextualReferencesIncludingDecl(ix, s.Name, s.File, s.Line)
		if !ok {
			complete = false
		}
		count += n
		for _, file := range f {
			if !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	sort.Strings(files)
	confidence := "textual"
	if !complete {
		confidence = "textual (scan truncated)"
	}
	return astrefs.Result{Files: files, Count: count, Confidence: confidence}
}
