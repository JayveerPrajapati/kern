package langparse

import (
	"go/ast"
	"go/token"
	"strings"
)

func CalleeName(fun ast.Expr) string {
	switch t := fun.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return CalleeName(t.X) + "." + t.Sel.Name
	case *ast.IndexExpr:
		return CalleeName(t.X)
	case *ast.IndexListExpr:
		return CalleeName(t.X)
	case *ast.ParenExpr:
		return CalleeName(t.X)
	case *ast.CallExpr:
		// Chained calls like json.NewEncoder(w).Encode(x): the outer call's
		// fun is a SelectorExpr whose X is the inner call. Recurse into the
		// inner call's function so the callee is "NewEncoder.Encode" rather
		// than the bare ".Encode" that previously made every chained call
		// unresolvable and cluttered the graph with ".Method" targets.
		return CalleeName(t.Fun)
	case *ast.TypeAssertExpr:
		return CalleeName(t.X)
	case *ast.StarExpr:
		return CalleeName(t.X)
	case *ast.UnaryExpr:
		return CalleeName(t.X)
	case *ast.CompositeLit:
		// Method call on a composite literal: jwt.MapClaims{...}.Encode() —
		// the receiver is the literal's type expression.
		return CalleeName(t.Type)
	}
	return ""
}

func ReceiverName(t ast.Expr) string {
	switch r := t.(type) {
	case *ast.Ident:
		return r.Name
	case *ast.StarExpr:
		return ReceiverName(r.X)
	case *ast.IndexExpr:
		return ReceiverName(r.X)
	case *ast.IndexListExpr:
		return ReceiverName(r.X)
	case *ast.SelectorExpr:
		return ReceiverName(r.Sel)
	}
	return ""
}

// returnTypeNames returns the declared result types of a function/method
// signature (e.g. ["*Store", "error"]), or nil when the function has none.

type LocalTypes map[string]string

func (lt LocalTypes) AddTypeField(names []*ast.Ident, typ ast.Expr) {
	if typ == nil {
		return
	}
	t := GoTypeReference(typ)
	for _, n := range names {
		if n.Name != "_" {
			lt[n.Name] = t
		}
	}
}

// CollectLocalTypes gathers receiver, parameter and short-variable declarations
// within one function body. retTypes maps same-file constructor names to their
// single return type so `x := New(...)` infers the real type (Index), not the
// constructor's name (New) — receiver-var method calls then resolve to
// "Index.M" instead of the dangling "New.M".
func CollectLocalTypes(fn *ast.FuncDecl, retTypes map[string]string) LocalTypes {
	lt := LocalTypes{}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			lt.AddTypeField(f.Names, f.Type)
		}
	}
	if fn.Type != nil && fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			lt.AddTypeField(f.Names, f.Type)
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
					if t := TypeNameOfExpr(s.Rhs[0], retTypes); t != "" {
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
					if t := TypeNameOfExpr(s.Rhs[i], retTypes); t != "" {
						lt[id.Name] = t
					}
				}
			}
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if s.Type != nil {
					lt.AddTypeField([]*ast.Ident{n}, s.Type)
				} else if len(s.Values) > i {
					if t := TypeNameOfExpr(s.Values[i], retTypes); t != "" {
						lt[n.Name] = t
					}
				}
			}
		}
		return true
	})
	return lt
}

// TypeNameOfExpr guesses the constructed type name from an initializer
// expression: T{}, &T{}, T(x), *T, new(T). A constructor call resolves to the
// constructor's single return type when it is declared in the same file
// (retTypes); otherwise it falls back to the constructor's own name (the
// previous behavior, which the alias layer in computeCallers still nets).
func TypeNameOfExpr(e ast.Expr, retTypes map[string]string) string {
	switch t := e.(type) {
	case *ast.CompositeLit:
		return GoTypeReference(t.Type)
	case *ast.UnaryExpr:
		if t.Op == token.AND {
			return TypeNameOfExpr(t.X, retTypes)
		}
	case *ast.CallExpr:
		if id, ok := t.Fun.(*ast.Ident); ok && id.Name == "new" {
			if len(t.Args) == 1 {
				return ReceiverName(t.Args[0])
			}
			return ""
		}
		if id, ok := t.Fun.(*ast.Ident); ok {
			if rt, ok := retTypes[id.Name]; ok && rt != "" {
				return rt
			}
		}
		return CalleeName(t.Fun)
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
func ResolveCallee(name string, lt LocalTypes) string {
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
