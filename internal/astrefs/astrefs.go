// Package astrefs provides AST-verified reference lookups for indexed Go
// files: the real reference set behind a type/const/var/field name, as opposed
// to refscan's bounded textual safety net.
package astrefs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/indexmem"
	"github.com/JayveerPrajapati/kern/internal/refscan"
)

// Reference is one AST-verified use of a symbol in an indexed Go file.
type Reference struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"column"`
	Kind string `json:"kind"` // call | type | selector | value
}

// scanBudget caps the bytes read while prefiltering files for the AST pass.
const scanBudget = 64 << 20

// References returns AST-verified references to name across the indexed Go
// files. Files are word-boundary prefiltered and only matches are parsed, so a
// symbol used in a handful of files costs a handful of parses. Declaration
// names are excluded (a symbol is not referenced by its own definition);
// comments and strings can never match because only the AST is walked.
// MemoizeScans enables per-index-generation memoization of reference scans.
//
// Off by default: the cache assumes an *Index is immutable once loaded, which
// is true for the daemon (every rebuild produces a new pointer) but NOT for a
// caller that edits an in-memory index between queries (tests, incremental
// tooling). Set it only in a warm, read-only process — cmd/kern's daemon.
var MemoizeScans bool

// complete is false when the read budget was exceeded.
func References(ix *index.Index, sym index.Symbol) ([]Reference, bool) {
	if !MemoizeScans {
		return referenceScan(ix, sym)
	}
	hit := indexmem.GenMemo(ix, "astrefs.refs", sym.Name+"\x00"+sym.File, func() refsAnswer {
		refs, complete := referenceScan(ix, sym)
		return refsAnswer{refs: refs, complete: complete}
	})
	return hit.refs, hit.complete
}

// refsAnswer is the memoized shape of a reference scan.
type refsAnswer struct {
	refs     []Reference
	complete bool
}

// referenceScan is the uncached AST scan behind References.
func referenceScan(ix *index.Index, sym index.Symbol) (refs []Reference, complete bool) {
	name := sym.Name
	if ix == nil || name == "" {
		return nil, true
	}
	// Name-resolved, not type-checked: outside the defining package a bare
	// identifier is far more likely a different same-named symbol, so only
	// package-qualified selectors (pkg.Name) count there. Struct fields are
	// always selected (x.Field), so any selector is kept for kind=field.
	pkg := packageName(ix.Root, sym.File)
	defDir := dirOf(sym.File)
	needle := []byte(name)
	scanned := 0
	complete = true
	files := make([]string, 0, len(ix.FileHashes))
	for rel := range ix.FileHashes {
		if strings.HasSuffix(rel, ".go") && !ix.GeneratedFiles[rel] {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(ix.Root, rel))
		if err != nil {
			continue
		}
		if scanned+len(src) > scanBudget {
			return refs, false
		}
		scanned += len(src)
		if refscan.CountWordBoundary(src, needle) == 0 {
			continue
		}
		refs = append(refs, fileReferences(rel, src, name, pkg, defDir, sym.Kind)...)
	}
	return refs, true
}

// Result is a reference lookup answer: the referencing files, the reference
// count, and how it was obtained.
type Result struct {
	Files      []string
	Count      int
	Confidence string // ast | ast (scan truncated) | textual | textual (scan truncated)
}

// Lookup answers a reference query with AST references when the Go scan finds
// any, falling back to refscan's bounded textual safety net otherwise.
func Lookup(ix *index.Index, sym index.Symbol) Result {
	if refs, complete := References(ix, sym); len(refs) > 0 {
		confidence := "ast"
		if !complete {
			confidence = "ast (scan truncated)"
		}
		return Result{Files: DistinctFiles(refs), Count: len(refs), Confidence: confidence}
	}
	files, count, complete := refscan.TextualReferencesIncludingDecl(ix, sym.Name, sym.File, sym.Line)
	confidence := "textual"
	if !complete {
		confidence = "textual (scan truncated)"
	}
	return Result{Files: files, Count: count, Confidence: confidence}
}

// DistinctFiles returns the sorted distinct file paths of refs.
func DistinctFiles(refs []Reference) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if !seen[r.File] {
			seen[r.File] = true
			out = append(out, r.File)
		}
	}
	sort.Strings(out)
	return out
}

func fileReferences(rel string, src []byte, name, pkg, defDir, symKind string) []Reference {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil
	}
	skip := declSkipSet(f)
	var refs []Reference
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name || skip[id] {
			return true
		}
		if !acceptRef(parent, id, rel, pkg, defDir, symKind) {
			return true
		}
		pos := fset.Position(id.Pos())
		refs = append(refs, Reference{File: rel, Line: pos.Line, Col: pos.Column, Kind: refKind(parent, id)})
		return true
	})
	return refs
}

func acceptRef(parent ast.Node, id *ast.Ident, rel, pkg, defDir, symKind string) bool {
	if dirOf(rel) == defDir || pkg == "" {
		return true
	}
	sel, ok := parent.(*ast.SelectorExpr)
	if !ok || sel.Sel != id {
		return false
	}
	if symKind == "field" {
		return true
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg
}

func dirOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// packageName reads the package clause of the defining file; empty when the
// file is unreadable or not Go.
func packageName(root, rel string) string {
	if !strings.HasSuffix(rel, ".go") {
		return ""
	}
	src, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.PackageClauseOnly)
	if err != nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}

// declSkipSet collects declaration identifiers (names being defined), which
// must never count as uses.
func declSkipSet(f *ast.File) map[*ast.Ident]bool {
	skip := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.FuncDecl:
			if t.Name != nil {
				skip[t.Name] = true
			}
		case *ast.TypeSpec:
			if t.Name != nil {
				skip[t.Name] = true
			}
		case *ast.ValueSpec:
			for _, nm := range t.Names {
				skip[nm] = true
			}
		case *ast.Field:
			for _, nm := range t.Names {
				skip[nm] = true
			}
		}
		return true
	})
	return skip
}

func refKind(parent ast.Node, id *ast.Ident) string {
	switch p := parent.(type) {
	case *ast.CallExpr:
		if p.Fun == id {
			return "call"
		}
	case *ast.SelectorExpr:
		if p.Sel == id {
			return "selector"
		}
	case *ast.TypeAssertExpr, *ast.CompositeLit, *ast.StarExpr, *ast.ArrayType,
		*ast.MapType, *ast.ChanType, *ast.IndexExpr, *ast.IndexListExpr, *ast.FuncType:
		return "type"
	}
	return "value"
}
