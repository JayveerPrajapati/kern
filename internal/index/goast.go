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
	Returns  []string `json:"returns,omitempty"` // declared return type names
	Lang     string   `json:"lang,omitempty"`
	// Confidence is the parser's reliability in this symbol: HIGH for direct
	// declarations (functions, types, vars), MEDIUM for inferred kinds, LOW
	// for heuristic detections such as regex-derived entry points. Absent
	// in indexes written by older kern (String() then reports LOW).
	Confidence Confidence `json:"confidence,omitempty"`
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
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Imports []ImportEdge `json:"imports"`
	Files   []string     `json:"files"`
	Lang    string       `json:"lang,omitempty"`
	// StructFields maps "StructName.fieldName" -> the bare type name of the
	// field's declared type (last identifier segment, pointer/array and
	// package qualifiers stripped), merged per package across files. The
	// merge-time callee rewrite uses it to complete receiver-field call
	// chains ("App.taskSvc.Deploy" -> "TaskService.Deploy") when the struct
	// is declared in a different file than the call. Absent in indexes
	// written by older kern: the rewrite then no-ops and the chain stays
	// alias-only, exactly as before.
	StructFields map[string]string `json:"struct_fields,omitempty"`
}

// extract parses a single Go file and returns its symbols, call edges and
// package info. rel is the path stored on records.
func extract(rel string, src []byte) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var syms []Symbol
	calls := make(map[string][]CallEdge)
	inherits := make(map[string][]string)

	// Same-file struct field types ("App.taskSvc" -> "TaskService"): merged
	// per package so the merge-time rewrite can complete receiver-field
	// chains ("a.taskSvc.Deploy") whose struct is declared in any file.
	sf := collectStructFields(f)

	// Same-file constructor return types (single return value only):
	// "func New(...) *Index" maps New -> Index so `x := New(...)` receiver
	// calls resolve to the real type.
	retTypes := map[string]string{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			if rts := returnTypeNames(fd.Type.Results); len(rts) > 0 {
				retTypes[fd.Name.Name] = rts[0]
			}
		}
	}

	addCalls := func(owner string, fn *ast.FuncDecl) {
		body := fn.Body
		if body == nil {
			return
		}
		lt := collectLocalTypes(fn, retTypes)
		ast.Inspect(body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			raw := calleeName(ce.Fun)
			name := resolveCallee(raw, lt)
			if name != "" && name != owner {
				// Direct syntactic call: HIGH. A callee that only resolved
				// through receiver/parameter/constructor type inference
				// ("x.M" -> "Type.M") is a method call recovered from local
				// types: MEDIUM.
				conf := ConfidenceHigh
				if name != raw {
					conf = ConfidenceMedium
				}
				calls[owner] = append(calls[owner], CallEdge{Target: name, Confidence: conf})
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
			returns := returnTypeNames(d.Type.Results)
			syms = append(syms, Symbol{Kind: kind, Name: name, Receiver: recv, File: rel, Line: fset.Position(d.Pos()).Line, End: fset.Position(d.End()).Line, Params: params, Returns: returns, Lang: "go", Confidence: ConfidenceHigh})
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
					syms = append(syms, Symbol{Kind: kind, Name: s.Name.Name, File: rel, Line: fset.Position(s.Pos()).Line, End: fset.Position(s.End()).Line, Lang: "go", Confidence: ConfidenceHigh})
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
						syms = append(syms, Symbol{Kind: kind, Name: name.Name, File: rel, Line: fset.Position(s.Pos()).Line, End: fset.Position(s.End()).Line, Lang: "go", Confidence: ConfidenceHigh})
					}
					// Package-level initializer calls ("var jsKw = kwSet(...)",
					// "var props = map[string]string{"prompt": strProp(...)}")
					// run at init time and are invisible to per-function call
					// walks, so the call graph records no callers for kwSet /
					// strProp — the deletion gate would wrongly report them
					// SAFE. Record initializer calls under the declared name.
					if d.Tok == token.VAR {
						for _, v := range s.Values {
							ast.Inspect(v, func(n ast.Node) bool {
								ce, ok := n.(*ast.CallExpr)
								if !ok {
									return true
								}
								target := resolveCallee(calleeName(ce.Fun), nil)
								if target == "" {
									return true
								}
								for _, name := range s.Names {
									if target != name.Name {
										calls[name.Name] = append(calls[name.Name], CallEdge{Target: target, Confidence: ConfidenceHigh})
									}
								}
								return true
							})
						}
					}
				}
			}
		}
	}

	pkg := &Pkg{Name: f.Name.Name, Path: filepath.Dir(rel), Files: []string{rel}, Lang: "go", StructFields: sf}
	for _, imp := range f.Imports {
		if imp.Path != nil {
			pkg.Imports = append(pkg.Imports, ImportEdge{Path: strings.Trim(imp.Path.Value, `"`), Confidence: ConfidenceHigh})
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
	case *ast.SelectorExpr:
		return receiverName(r.Sel)
	}
	return ""
}

// returnTypeNames returns the declared result types of a function/method
// signature (e.g. ["*Store", "error"]), or nil when the function has none.
func returnTypeNames(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		n := receiverName(f.Type)
		if n == "" {
			if se, ok := f.Type.(*ast.SelectorExpr); ok && se.Sel != nil {
				n = se.Sel.Name
			}
		}
		if n == "" {
			continue
		}
		if len(f.Names) == 0 {
			out = append(out, n)
			continue
		}
		// named results ("(s *Store, err error)"): record the type once per
		// result name for callers that match by position.
		for range f.Names {
			out = append(out, n)
		}
	}
	return out
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

// collectStructFields gathers "StructName.fieldName" -> bare field type name
// from every struct declaration in the file. Embedded fields (no field name)
// use the type's own name as the field name. Pointer/array/package-qualified
// wrappers are stripped to the final identifier segment, matching the bare
// names the call graph uses. Returns nil when the file declares no struct
// fields.
func collectStructFields(f *ast.File) map[string]string {
	var out map[string]string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			for _, fd := range st.Fields.List {
				t := receiverName(fd.Type)
				if t == "" {
					continue
				}
				if out == nil {
					out = map[string]string{}
				}
				if len(fd.Names) == 0 {
					// Embedded field: the field's Go name is the type name.
					out[ts.Name.Name+"."+t] = t
					continue
				}
				for _, n := range fd.Names {
					if n.Name != "_" {
						out[ts.Name.Name+"."+n.Name] = t
					}
				}
			}
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
// within one function body. retTypes maps same-file constructor names to their
// single return type so `x := New(...)` infers the real type (Index), not the
// constructor's name (New) — receiver-var method calls then resolve to
// "Index.M" instead of the dangling "New.M".
func collectLocalTypes(fn *ast.FuncDecl, retTypes map[string]string) localTypes {
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
			// `x := New(...)` and the multi-value idiom
			// `gov, err := s.newGovernor(...)`: the first Lhs takes the
			// call's inferred type. (Before, multi-value assigns were
			// skipped entirely, leaving receiver-var edges dangling on the
			// variable name.)
			if len(s.Rhs) == 1 {
				if id, ok := s.Lhs[0].(*ast.Ident); ok {
					if t := typeNameOfExpr(s.Rhs[0], retTypes); t != "" {
						lt[id.Name] = t
					}
				}
				break
			}
			for i := range s.Lhs {
				if i >= len(s.Rhs) {
					break
				}
				if id, ok := s.Lhs[i].(*ast.Ident); ok {
					if t := typeNameOfExpr(s.Rhs[i], retTypes); t != "" {
						lt[id.Name] = t
					}
				}
			}
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if s.Type != nil {
					lt.addTypeField([]*ast.Ident{n}, s.Type)
				} else if len(s.Values) > i {
					if t := typeNameOfExpr(s.Values[i], retTypes); t != "" {
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
// expression: T{}, &T{}, T(x), *T, new(T). A constructor call resolves to the
// constructor's single return type when it is declared in the same file
// (retTypes); otherwise it falls back to the constructor's own name (the
// previous behavior, which the alias layer in computeCallers still nets).
func typeNameOfExpr(e ast.Expr, retTypes map[string]string) string {
	switch t := e.(type) {
	case *ast.CompositeLit:
		return receiverName(t.Type)
	case *ast.UnaryExpr:
		if t.Op == token.AND {
			return typeNameOfExpr(t.X, retTypes)
		}
	case *ast.CallExpr:
		if id, ok := t.Fun.(*ast.Ident); ok && id.Name == "new" {
			if len(t.Args) == 1 {
				return receiverName(t.Args[0])
			}
			return ""
		}
		if id, ok := t.Fun.(*ast.Ident); ok {
			if rt, ok := retTypes[id.Name]; ok && rt != "" {
				return rt
			}
		}
		return calleeName(t.Fun)
	}
	return ""
}

// resolveCallee rewrites a receiver-var method call to its type-qualified form
// when the receiver variable's type is known locally: v.M() -> T.M. Calls on
// variables with unknown or external types are left untouched.
// resolveCallee rewrites a recorded callee name against the enclosing
// function's local types.
//
// Two shapes are resolved:
//   - receiver/var method call: "x.M" with lt[x]="T" -> "T.M";
//   - receiver-field chain: "a.taskSvc.Deploy" with lt[a]="App" resolves the
//     FIRST segment only, giving "App.taskSvc.Deploy".
//
// The merge-time rewrite (rewriteConstructorCallees) completes the chain
// against the package's merged struct-field map and the project's declared
// type set — the authoritative, guarded pass: extract-time resolution must
// stay conservative because it cannot know whether a field's type is
// declared in the project.
func resolveCallee(name string, lt localTypes) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name
	}
	prefix, sel := name[:i], name[i+1:]
	if t, ok := lt[prefix]; ok && t != "" {
		return t + "." + sel
	}
	if j := strings.IndexByte(name, '.'); j > 0 && j < len(name)-1 {
		if t, ok := lt[name[:j]]; ok && t != "" {
			return t + "." + name[j+1:]
		}
	}
	return name
}
