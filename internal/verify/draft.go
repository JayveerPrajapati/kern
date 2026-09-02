package verify

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// DraftFinding is one validation result for a draft code snippet.
type DraftFinding struct {
	Line    int    `json:"line"`
	Kind    string `json:"kind"` // "parse_error" | "unknown_import" | "unknown_symbol" | "unknown_method"
	Message string `json:"message"`
}

// goBuiltins are Go predeclared identifiers that are valid call targets
// without being declared in the draft or indexed in the project.
var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

// CheckDraft validates a draft code snippet against the project index.
// Go code (lang "" or "go") is parsed with go/parser and checked
// structurally; other languages get the conservative checks only
// (unknown_import for relative paths). Deterministic — no LLM.
//
// Checks performed for Go:
//   - parse errors (single parse_error finding; checking stops),
//   - relative imports that do not resolve to a real directory under root,
//   - calls to simple identifiers that are neither declared in the draft, nor
//     Go builtins, nor present in the index symbol table,
//   - selector calls on an import alias whose target method is not found in
//     the indexed package (packages unknown to the index, e.g. stdlib, are
//     skipped silently).
//
// Known conservative limitations (v1): a call to a symbol declared in a
// sibling file of the same package that is not in the index is reported as
// unknown_symbol — an accepted false positive, since the draft is validated
// standalone against the index. Function-local scope is not tracked (all
// declared names form one set), so a call to another function's local is a
// false negative, not a false positive. Non-Go languages return no findings:
// the structural checks are Go-only in v1.
func CheckDraft(ix *index.Index, root string, code []byte, lang string) []DraftFinding {
	// Non-Go languages are skipped conservatively (v1 is Go-only).
	if lang != "" && lang != "go" {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "draft.go", code, 0)
	if err != nil {
		return []DraftFinding{parseFinding(err)}
	}

	var findings []DraftFinding

	// Import checks: relative imports must resolve to a real directory under
	// root; absolute imports are skipped (stdlib vs third-party needs module
	// resolution). Also record each import's alias for selector-call checks.
	aliases := map[string]string{} // import alias/last segment -> import path
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		line := fset.Position(imp.Pos()).Line
		if strings.HasPrefix(path, ".") {
			if _, serr := os.Stat(filepath.Join(root, path)); serr != nil {
				findings = append(findings, DraftFinding{
					Line:    line,
					Kind:    "unknown_import",
					Message: fmt.Sprintf("relative import %q does not exist under root", path),
				})
			}
		}
		aliases[importBaseName(imp, path)] = path
	}

	// Local symbol set: every name declared anywhere in the draft.
	locals := collectLocalSymbols(f)

	// Call-target checks, in AST order.
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		line := fset.Position(ce.Pos()).Line
		switch fun := ce.Fun.(type) {
		case *ast.Ident:
			// Simple call: builtin, local or indexed symbol, else unknown.
			if goBuiltins[fun.Name] || locals[fun.Name] || indexHasSymbol(ix, fun.Name) {
				return true
			}
			findings = append(findings, DraftFinding{
				Line:    line,
				Kind:    "unknown_symbol",
				Message: fmt.Sprintf("call to unknown symbol %q", fun.Name),
			})
		case *ast.SelectorExpr:
			// Selector call on an import alias: verify the method exists in
			// the indexed package. Selector calls on variables/receivers need
			// type resolution and are skipped.
			x, ok := fun.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, isAlias := aliases[x.Name]
			if !isAlias {
				return true
			}
			method := fun.Sel.Name
			syms, known := indexPackageSymbols(ix, path)
			if !known {
				return true // package unknown to the index (e.g. stdlib) — skip
			}
			for _, s := range syms {
				if s.Name == method {
					return true
				}
			}
			findings = append(findings, DraftFinding{
				Line:    line,
				Kind:    "unknown_method",
				Message: fmt.Sprintf("%q not found in index", x.Name+"."+method),
			})
		}
		return true
	})

	return findings
}

// parseFinding builds a parse_error finding, extracting the error position
// line when the parser provides one.
func parseFinding(err error) DraftFinding {
	line := 0
	if el, ok := err.(scanner.ErrorList); ok && len(el) > 0 {
		line = el[0].Pos.Line
	} else if se, ok := err.(*scanner.Error); ok {
		line = se.Pos.Line
	}
	return DraftFinding{Kind: "parse_error", Line: line, Message: err.Error()}
}

// importBaseName returns the name an import is referenced by in the file: an
// explicit alias, or the last path segment.
func importBaseName(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// collectLocalSymbols returns the set of every name declared in the file:
// functions, methods (qualified "Recv.Method"), types, vars/consts,
// parameters, named results, and := / var locals inside bodies. Scope is not
// tracked in v1 — the whole file forms one set.
func collectLocalSymbols(f *ast.File) map[string]bool {
	locals := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			locals[d.Name.Name] = true
			if d.Recv != nil {
				for _, rf := range d.Recv.List {
					if id, ok := rf.Type.(*ast.Ident); ok {
						locals[id.Name+"."+d.Name.Name] = true
					}
				}
			}
			if d.Type != nil {
				addFieldNames(d.Type.Params, locals)
				addFieldNames(d.Type.Results, locals)
			}
		case *ast.FuncLit:
			if d.Type != nil {
				addFieldNames(d.Type.Params, locals)
				addFieldNames(d.Type.Results, locals)
			}
		case *ast.TypeSpec:
			locals[d.Name.Name] = true
		case *ast.ValueSpec:
			for _, id := range d.Names {
				locals[id.Name] = true
			}
		case *ast.AssignStmt:
			if d.Tok == token.DEFINE {
				for _, lhs := range d.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						locals[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return locals
}

func addFieldNames(fl *ast.FieldList, locals map[string]bool) {
	if fl == nil {
		return
	}
	for _, fld := range fl.List {
		for _, n := range fld.Names {
			locals[n.Name] = true
		}
	}
}

// indexHasSymbol reports whether the index contains a symbol named name,
// matching by exact Name or (for exported symbols) by FullName — the same
// lookup idiom Verify uses for bare identifiers.
func indexHasSymbol(ix *index.Index, name string) bool {
	if ix == nil {
		return false
	}
	for _, s := range ix.Symbols {
		if s.Name == name || (name == s.FullName() && isExported(s.Name)) {
			return true
		}
	}
	return false
}

// indexPackageSymbols returns every index symbol belonging to the package an
// import path refers to, and whether the package is known to the index. The
// path is matched exactly against index package paths and, failing that, by
// its last path segment (import paths carry module prefixes the index does
// not, e.g. "example.com/mod/db" -> indexed package "db"). Deterministic:
// candidate packages are collected in sorted key order.
func indexPackageSymbols(ix *index.Index, importPath string) ([]index.Symbol, bool) {
	if ix == nil {
		return nil, false
	}
	clean := strings.TrimPrefix(importPath, "./")
	base := clean
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	keys := make([]string, 0, len(ix.Pkgs))
	for k := range ix.Pkgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var files []string
	matched := false
	for _, k := range keys {
		kb := k
		if i := strings.LastIndex(kb, "/"); i >= 0 {
			kb = kb[i+1:]
		}
		if k == clean || kb == base {
			matched = true
			files = append(files, ix.Pkgs[k].Files...)
		}
	}
	if !matched {
		return nil, false
	}
	fileSet := map[string]bool{}
	for _, f := range files {
		fileSet[f] = true
	}
	var out []index.Symbol
	for _, s := range ix.Symbols {
		if fileSet[s.File] {
			out = append(out, s)
		}
	}
	return out, true
}
