package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/asttypes"
	"github.com/JayveerPrajapati/kern/internal/calleefilter"
	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// Symbol and Pkg are aliases of the shared AST types (internal/asttypes).
type (
	Symbol = asttypes.Symbol
	Pkg    = asttypes.Pkg
)

// localTypes aliases langparse.LocalTypes so the extract walk below can
// keep its callback signature. The map, its collector and the callee
// resolvers moved to langparse with the go/ast call machinery.
type localTypes = langparse.LocalTypes

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
		langparse.WalkGoCalls(fn, retTypes, func(ce *ast.CallExpr, lt localTypes) {
			raw := langparse.CalleeName(ce.Fun)
			name := langparse.ResolveCallee(raw, lt)
			prefix := strings.SplitN(raw, ".", 2)[0]
			reference, local := lt[prefix]
			dynamic := local && (reference == "" || !strings.Contains(raw, "."))
			if name != "" && name != owner && !calleefilter.IsNoiseCalleeFor(raw, calleefilter.ChainHasCall(ce.Fun), rel) {
				// Direct syntactic call: HIGH. A callee that only resolved
				// through receiver/parameter/constructor type inference
				// ("x.M" -> "Type.M") is a method call recovered from local
				// types: MEDIUM.
				conf := ConfidenceHigh
				if name != raw {
					conf = ConfidenceMedium
				}
				calls[owner] = append(calls[owner], CallEdge{Target: name, Confidence: conf, Dynamic: dynamic})
			}
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
				recv = langparse.ReceiverName(d.Recv.List[0].Type)
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
			// Router-table registrations are not syntactic calls; synthesize the
			// dispatch hop so path/impact/probe traverse the framework boundary.
			synthesizeDispatchEdges(full, d.Body, calls)
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
							if signature, ok := m.Type.(*ast.FuncType); ok {
								for _, name := range m.Names {
									syms = append(syms, Symbol{Kind: "method", Name: name.Name, Receiver: s.Name.Name, File: rel, Line: fset.Position(m.Pos()).Line, End: fset.Position(m.End()).Line, Params: paramNames(signature.Params), Returns: returnTypeNames(signature.Results), Lang: "go", Confidence: ConfidenceHigh})
								}
							}
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
								target := langparse.ResolveCallee(langparse.CalleeName(ce.Fun), nil)
								if target == "" || calleefilter.IsNoiseCalleeFor(langparse.CalleeName(ce.Fun), calleefilter.ChainHasCall(ce.Fun), rel) {
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

	pkg := &Pkg{Name: f.Name.Name, Path: filepath.Dir(rel), Files: []string{rel}, Lang: "go", StructFields: sf, Constructors: retTypes}
	for _, imp := range f.Imports {
		if imp.Path != nil {
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			pkg.Imports = append(pkg.Imports, ImportEdge{Path: strings.Trim(imp.Path.Value, `"`), Confidence: ConfidenceHigh, Alias: alias})
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

func returnTypeNames(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		n := langparse.ReceiverName(f.Type)
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
				t := langparse.ReceiverName(fd.Type)
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
