package index

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

var httpVerbs = map[string]bool{
	// All-caps (gin/echo) and title-case (chi r.Get, fiber app.Get) forms.
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	"HEAD": true, "OPTIONS": true, "USE": true, "ALL": true, "CONNECT": true, "TRACE": true,
	"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true,
	"Head": true, "Options": true, "Use": true, "All": true, "Connect": true, "Trace": true,
}

// extractGoEntries finds HTTP entry points in a Go file: http.Handle/HandleFunc
// registrations, ServeMux handlers, and verb-method router calls (gin/echo/chi/
// fiber). It enriches resolvable handler symbols and returns standalone entry
// markers for the rest.
func extractGoEntries(fset *token.FileSet, f *ast.File, syms []Symbol, rel string) []Symbol {
	var extra []Symbol
	pathOf := func(arg ast.Expr) string {
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return s
			}
		}
		return ""
	}
	handlerName := func(arg ast.Expr) string {
		switch t := arg.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.SelectorExpr:
			return t.Sel.Name
		}
		return ""
	}
	visit := func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := fn.Sel.Name
		var fwID string
		switch {
		case method == "Handle" || method == "HandleFunc":
			if _, isVar := fn.X.(*ast.Ident); !isVar {
				return true
			}
			fwID = "net-http"
		case httpVerbs[method]:
			fwID = "http-route"
		default:
			return true
		}
		if len(ce.Args) < 1 {
			return true
		}
		r := pathOf(ce.Args[0])
		if !langparse.RouteOK(r) {
			return true
		}
		name := ""
		if len(ce.Args) >= 2 {
			name = handlerName(ce.Args[1])
		}
		if name == "" {
			name = method + " " + r
		}
		if !langparse.EnrichEntry(syms, rel, name, fwID, r) {
			extra = append(extra, langparse.EntrySym(rel, "go", fwID, name, r, fset.Position(ce.Pos()).Line))
		}
		return true
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				ast.Inspect(d.Body, visit)
			}
		case *ast.GenDecl:
			ast.Inspect(d, visit)
		}
	}
	return extra
}

// resolveEntries links standalone entry markers to the handler symbols they
// refer to. Handlers often live in a different file than the route that
// registers them, so a single-file extraction cannot enrich them directly;
// this pass runs after the whole project has been indexed.
func (ix *Index) resolveEntries() {
	byName := map[string]int{}
	byFull := map[string]int{}
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		if _, ok := byFull[s.FullName()]; !ok {
			byFull[s.FullName()] = i
		}
		if _, ok := byName[s.Name]; !ok {
			byName[s.Name] = i
		}
	}
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Kind != "entry" || s.Name == "" {
			continue
		}
		ti := -1
		if t, ok := byFull[s.FullName()]; ok {
			ti = t
		} else if t, ok := byName[s.Name]; ok {
			ti = t
		}
		if ti >= 0 {
			if ix.Symbols[ti].Framework == "" {
				ix.Symbols[ti].Framework = s.Framework
			}
			if ix.Symbols[ti].Route == "" && s.Route != "" {
				ix.Symbols[ti].Route = s.Route
			}
			ix.Symbols[ti].Entry = true
		}
	}
}
