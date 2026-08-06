// Package index builds a persistent AST-level index of a Go project: symbols,
// imports, call edges, and reverse callers. It powers kern's AST search, code
// graph and minimal-context-slice tools.
package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// Symbol is one top-level declaration.
type Symbol struct {
	Kind     string   `json:"kind"` // func, method, struct, interface, type, const, var
	Name     string   `json:"name"`
	Receiver string   `json:"receiver,omitempty"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	End      int      `json:"end,omitempty"` // inclusive last line (0 if unknown)
	Params   []string `json:"params,omitempty"`
	Lang     string   `json:"lang,omitempty"`
	// Framework-aware entry-point metadata: a symbol with Entry set is a
	// framework entry point (HTTP handler, route, controller endpoint, task).
	Entry     bool   `json:"entry,omitempty"`
	Framework string `json:"framework,omitempty"` // fw framework id, e.g. "spring-mvc"
	Route     string `json:"route,omitempty"`     // route/path the entry serves, e.g. "/users"
}

// Lines returns the 1-based size of the declaration in source lines, falling
// back to a single line when the end is unknown.
func (s Symbol) Lines() int {
	if s.End > 0 && s.End >= s.Line {
		return s.End - s.Line + 1
	}
	return 1
}

// FullName returns the qualified name ("Type.Method" for methods).
func (s Symbol) FullName() string {
	if s.Receiver != "" {
		return s.Receiver + "." + s.Name
	}
	return s.Name
}

// Pkg is one package/module discovered in the project.
type Pkg struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Imports []string `json:"imports"`
	Files   []string `json:"files"`
	Lang    string   `json:"lang,omitempty"`
}

// extract parses a single Go file and returns its symbols, call edges and
// package info. rel is the path stored on records.
func extract(rel string, src []byte) ([]Symbol, map[string][]string, *Pkg, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	var syms []Symbol
	calls := make(map[string][]string)

	addCalls := func(owner string, body ast.Node) {
		if body == nil {
			return
		}
		ast.Inspect(body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeName(ce.Fun)
			if name != "" && name != owner {
				calls[owner] = append(calls[owner], name)
			}
			return true
		})
	}

	// Only top-level declarations become symbols; function-local vars and
	// types are implementation detail and would pollute graph analysis.
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			recv := ""
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recv = receiverName(d.Recv.List[0].Type)
				kind = "method"
			}
			full := name
			if recv != "" {
				full = recv + "." + name
			}
			params := paramNames(d.Type.Params)
			syms = append(syms, Symbol{Kind: kind, Name: name, Receiver: recv, File: rel, Line: fset.Position(d.Pos()).Line, End: fset.Position(d.End()).Line, Params: params, Lang: "go"})
			addCalls(full, d.Body)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind := "type"
					switch s.Type.(type) {
					case *ast.StructType:
						kind = "struct"
					case *ast.InterfaceType:
						kind = "interface"
					}
					syms = append(syms, Symbol{Kind: kind, Name: s.Name.Name, File: rel, Line: fset.Position(s.Pos()).Line, End: fset.Position(s.End()).Line, Lang: "go"})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						syms = append(syms, Symbol{Kind: kind, Name: name.Name, File: rel, Line: fset.Position(s.Pos()).Line, End: fset.Position(s.End()).Line, Lang: "go"})
					}
				}
			}
		}
	}

	pkg := &Pkg{Name: f.Name.Name, Path: filepath.Dir(rel), Files: []string{rel}, Lang: "go"}
	for _, imp := range f.Imports {
		if imp.Path != nil {
			pkg.Imports = append(pkg.Imports, strings.Trim(imp.Path.Value, `"`))
		}
	}
	syms = append(syms, extractGoEntries(fset, f, syms, rel)...)
	return syms, calls, pkg, nil
}

func calleeName(fun ast.Expr) string {
	switch t := fun.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return calleeName(t.X) + "." + t.Sel.Name
	case *ast.IndexExpr:
		return calleeName(t.X)
	case *ast.IndexListExpr:
		return calleeName(t.X)
	case *ast.ParenExpr:
		return calleeName(t.X)
	}
	return ""
}

func receiverName(t ast.Expr) string {
	switch r := t.(type) {
	case *ast.Ident:
		return r.Name
	case *ast.StarExpr:
		return receiverName(r.X)
	case *ast.IndexExpr:
		return receiverName(r.X)
	case *ast.IndexListExpr:
		return receiverName(r.X)
	}
	return ""
}

func paramNames(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
	}
	return out
}
