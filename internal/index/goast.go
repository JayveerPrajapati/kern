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
func extract(rel string, src []byte) ([]Symbol, map[string][]string, map[string][]string, *Pkg, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var syms []Symbol
	calls := make(map[string][]string)
	inherits := make(map[string][]string)

	addCalls := func(owner string, fn *ast.FuncDecl) {
		body := fn.Body
		if body == nil {
			return
		}
		lt := collectLocalTypes(fn)
		ast.Inspect(body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := resolveCallee(calleeName(ce.Fun), lt)
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
			addCalls(full, d)
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
					// Interface embedding ("type Reader interface { io.Reader }")
					// and struct embedding ("type T struct { Base }") are
					// inheritance edges.
					switch t := s.Type.(type) {
					case *ast.InterfaceType:
						for _, m := range t.Methods.List {
							for _, base := range embeddedNames(m.Type) {
								inherits[s.Name.Name] = append(inherits[s.Name.Name], "embeds:"+base)
							}
						}
					case *ast.StructType:
						for _, fld := range t.Fields.List {
							if fld.Names == nil {
								for _, base := range embeddedNames(fld.Type) {
									inherits[s.Name.Name] = append(inherits[s.Name.Name], "embeds:"+base)
								}
							}
						}
					}
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
	return syms, calls, inherits, pkg, nil
}

// embeddedNames returns the base type names embedded in an interface method
// field or struct field with no field name ("io.Reader", "Base", "*P", or
// "T[A,B]" strips to the base).
func embeddedNames(t ast.Expr) []string {
	switch e := t.(type) {
	case *ast.Ident:
		return []string{e.Name}
	case *ast.SelectorExpr:
		base := embeddedNames(e.X)
		if len(base) > 0 {
			return []string{base[len(base)-1]}
		}
		return nil
	case *ast.StarExpr:
		return embeddedNames(e.X)
	case *ast.IndexExpr:
		return embeddedNames(e.X)
	case *ast.IndexListExpr:
		return embeddedNames(e.X)
	case *ast.ParenExpr:
		return embeddedNames(e.X)
	}
	return nil
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

// localTypes maps a bare identifier (receiver, parameter or declared variable)
// to the type name it was declared with, gathered from the enclosing function
// so receiver-var method calls like v.M() can be linked to the type's method
// T.M instead of dangling on the variable name.
type localTypes map[string]string

func (lt localTypes) addTypeField(names []*ast.Ident, typ ast.Expr) {
	if typ == nil {
		return
	}
	t := receiverName(typ)
	if t == "" {
		return
	}
	for _, n := range names {
		if n.Name != "_" {
			lt[n.Name] = t
		}
	}
}

// collectLocalTypes gathers receiver, parameter and short-variable declarations
// within one function body.
func collectLocalTypes(fn *ast.FuncDecl) localTypes {
	lt := localTypes{}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			lt.addTypeField(f.Names, f.Type)
		}
	}
	if fn.Type != nil && fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			lt.addTypeField(f.Names, f.Type)
		}
	}
	if fn.Body == nil {
		return lt
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
				if id, ok := s.Lhs[0].(*ast.Ident); ok {
					if t := typeNameOfExpr(s.Rhs[0]); t != "" {
						lt[id.Name] = t
					}
				}
			}
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if s.Type != nil {
					lt.addTypeField([]*ast.Ident{n}, s.Type)
				} else if len(s.Values) > i {
					if t := typeNameOfExpr(s.Values[i]); t != "" {
						lt[n.Name] = t
					}
				}
			}
		}
		return true
	})
	return lt
}

// typeNameOfExpr guesses the constructed type name from an initializer
// expression: T{}, &T{}, T(x), *T, new(T).
func typeNameOfExpr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.CompositeLit:
		return receiverName(t.Type)
	case *ast.UnaryExpr:
		if t.Op == token.AND {
			return typeNameOfExpr(t.X)
		}
	case *ast.CallExpr:
		if id, ok := t.Fun.(*ast.Ident); ok && id.Name == "new" {
			if len(t.Args) == 1 {
				return receiverName(t.Args[0])
			}
			return ""
		}
		return calleeName(t.Fun)
	}
	return ""
}

// resolveCallee rewrites a receiver-var method call to its type-qualified form
// when the receiver variable's type is known locally: v.M() -> T.M. Calls on
// variables with unknown or external types are left untouched.
func resolveCallee(name string, lt localTypes) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name
	}
	prefix, sel := name[:i], name[i+1:]
	if t, ok := lt[prefix]; ok && t != "" {
		return t + "." + sel
	}
	return name
}
