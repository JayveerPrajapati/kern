package intel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// CheckPurity validates @pure-annotated Go functions: a function/method whose
// doc comment contains "@pure" must not mutate anything outside its own stack
// frame. Checks (all mechanical, go/ast):
//   - assignment/incdec to a package-level variable
//   - assignment to a receiver field (methods)
//   - mutation through a pointer parameter or the receiver (e.g. *p = ..., p.x = ... when p is a param/recv)
//   - channel sends
//
// Transitive purity (calling another impure function) is NOT checked in v1.
// Files that are not Go or fail to parse are skipped. Deterministic: files in
// input order, functions in source order, violations in AST order.
func CheckPurity(ix *index.Index, files []string) []Violation {
	var violations []Violation
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		src, ok := readSource(ix, f)
		if !ok {
			continue
		}
		fset := token.NewFileSet()
		fileAst, err := parser.ParseFile(fset, f, src, parser.ParseComments)
		if err != nil {
			// Unparseable files are skipped, never a failure.
			continue
		}
		pkgVars := packageVars(fileAst, ix, f)
		for _, decl := range fileAst.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isPureAnnotated(fn) {
				continue
			}
			symbol := fnSymbolName(fn)
			recv := receiverIdent(fn)
			scope := scopeNames(fn)  // params + named results (shadow package vars)
			locals := bodyLocals(fn) // names introduced by := or var inside the body
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch st := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range st.Lhs {
						if v := purityLHSViolation(f, symbol, lhs, pkgVars, scope, recv, locals, fset); v != nil {
							violations = append(violations, *v)
						}
					}
				case *ast.IncDecStmt:
					if v := purityLHSViolation(f, symbol, st.X, pkgVars, scope, recv, locals, fset); v != nil {
						violations = append(violations, *v)
					}
				case *ast.SendStmt:
					if v := puritySendViolation(f, symbol, st, recv, fset); v != nil {
						violations = append(violations, *v)
					}
				}
				return true
			})
		}
	}
	return violations
}

// readSource loads a file relative to the index root (or "." when the index
// is nil, mirroring how guard resolves files), reporting whether it succeeded.
func readSource(ix *index.Index, f string) ([]byte, bool) {
	root := "."
	if ix != nil && ix.Root != "" {
		root = ix.Root
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
	if err != nil {
		return nil, false
	}
	return src, true
}

// packageVars collects the package-level variable names visible to a file: the
// file's own top-level `var` declarations plus (when the index is available)
// every "var"-kind symbol declared in a sibling file of the same directory.
// This catches `var counter int` declared in a sibling file.
func packageVars(fileAst *ast.File, ix *index.Index, f string) map[string]bool {
	vars := map[string]bool{}
	for _, decl := range fileAst.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, name := range vs.Names {
					vars[name.Name] = true
				}
			}
		}
	}
	if ix != nil {
		for _, s := range ix.Symbols {
			if s.Kind == "var" && filepath.Dir(s.File) == filepath.Dir(f) {
				vars[s.Name] = true
			}
		}
	}
	return vars
}

// isPureAnnotated reports whether a function's doc comment contains the
// case-insensitive substring "@pure".
func isPureAnnotated(fn *ast.FuncDecl) bool {
	return fn.Doc != nil && strings.Contains(strings.ToLower(fn.Doc.Text()), "@pure")
}

// fnSymbolName returns the violation Symbol: the function name, or
// "ReceiverType.Method" for methods (receiver type without package/star).
func fnSymbolName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if recvType := receiverTypeName(fn.Recv.List[0].Type); recvType != "" {
			return recvType + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

// receiverTypeName strips stars, index expressions, and package qualifiers
// from a receiver type expression ("*pkg.Thing[T]" -> "Thing").
func receiverTypeName(t ast.Expr) string {
	switch r := t.(type) {
	case *ast.Ident:
		return r.Name
	case *ast.StarExpr:
		return receiverTypeName(r.X)
	case *ast.IndexExpr:
		return receiverTypeName(r.X)
	case *ast.IndexListExpr:
		return receiverTypeName(r.X)
	case *ast.SelectorExpr:
		return receiverTypeName(r.Sel)
	}
	return ""
}

// receiverIdent returns the receiver's identifier name ("t" for
// "func (t *Thing) M()"), or "" for plain functions.
func receiverIdent(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	names := fn.Recv.List[0].Names
	if len(names) == 0 {
		return ""
	}
	return names[0].Name
}

// scopeNames collects the names scoped directly to the function signature —
// parameters and named results. Writes to these are local to the frame, and
// they shadow package-level variables of the same name.
func scopeNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	if fn.Type.Params != nil {
		for _, fld := range fn.Type.Params.List {
			for _, name := range fld.Names {
				names[name.Name] = true
			}
		}
	}
	if fn.Type.Results != nil {
		for _, fld := range fn.Type.Results.List {
			for _, name := range fld.Names {
				names[name.Name] = true
			}
		}
	}
	return names
}

// bodyLocals collects names introduced by `:=` or `var` statements anywhere in
// the function body. These shadow both package vars and param/receiver names
// in their block, so writes to them never escape the frame.
func bodyLocals(fn *ast.FuncDecl) map[string]bool {
	locals := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.AssignStmt:
			if st.Tok == token.DEFINE {
				for _, lhs := range st.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						locals[id.Name] = true
					}
				}
			}
		case *ast.GenDecl:
			if st.Tok == token.VAR {
				for _, spec := range st.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							locals[name.Name] = true
						}
					}
				}
			}
		}
		return true
	})
	return locals
}

// purityLHSViolation flags one assignment/incdec target that escapes the
// function's stack frame, or nil when the write is purely local.
func purityLHSViolation(f, symbol string, lhs ast.Expr, pkgVars, scope map[string]bool, recv string, locals map[string]bool, fset *token.FileSet) *Violation {
	mk := func(ruleTo string) *Violation {
		return &Violation{
			CallerFile: f,
			Symbol:     symbol,
			Line:       fset.Position(lhs.Pos()).Line,
			RuleFrom:   "@pure",
			RuleTo:     ruleTo,
		}
	}
	switch l := lhs.(type) {
	case *ast.Ident:
		// Assignment/incdec to a package-level variable (unless shadowed by a
		// parameter, named result, or body-local of the same name).
		if pkgVars[l.Name] && !scope[l.Name] && l.Name != recv && !locals[l.Name] {
			return mk("var:" + l.Name)
		}
	case *ast.SelectorExpr:
		// Receiver field write: t.Field = ... (or t.Field++).
		if x, ok := l.X.(*ast.Ident); ok && x.Name == recv && !locals[x.Name] {
			return mk("receiver:" + l.Sel.Name)
		}
		// Fall through: a selector rooted in a param/receiver, e.g. p.Field = ...
		root := rootIdent(l)
		if root != nil && isParamOrRecv(root, scope, recv) && !locals[root.Name] {
			return mk(paramOrRecvRule(root, scope, recv))
		}
	case *ast.StarExpr, *ast.IndexExpr, *ast.IndexListExpr:
		// *p = ..., (*p).x = ..., arr[i] = ... — mutation through a pointer
		// parameter or the receiver.
		root := rootIdent(l)
		if root != nil && isParamOrRecv(root, scope, recv) && !locals[root.Name] {
			return mk(paramOrRecvRule(root, scope, recv))
		}
	}
	return nil
}

// puritySendViolation flags a channel send: RuleTo "chan:<ident>" for a bare
// channel identifier, or "receiver:<sel>" when sending on a receiver field.
func puritySendViolation(f, symbol string, st *ast.SendStmt, recv string, fset *token.FileSet) *Violation {
	mk := func(ruleTo string) *Violation {
		return &Violation{
			CallerFile: f,
			Symbol:     symbol,
			Line:       fset.Position(st.Pos()).Line,
			RuleFrom:   "@pure",
			RuleTo:     ruleTo,
		}
	}
	if sel, ok := st.Chan.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == recv {
			return mk("receiver:" + sel.Sel.Name)
		}
	}
	if id := rootIdent(st.Chan); id != nil {
		return mk("chan:" + id.Name)
	}
	return nil
}

// rootIdent unwraps StarExpr/IndexExpr/IndexListExpr/SelectorExpr chains to the
// root identifier of an expression ("(*p).x[0]" -> p).
func rootIdent(e ast.Expr) *ast.Ident {
	switch x := e.(type) {
	case *ast.Ident:
		return x
	case *ast.StarExpr:
		return rootIdent(x.X)
	case *ast.IndexExpr:
		return rootIdent(x.X)
	case *ast.IndexListExpr:
		return rootIdent(x.X)
	case *ast.SelectorExpr:
		return rootIdent(x.X)
	}
	return nil
}

func isParamOrRecv(root *ast.Ident, scope map[string]bool, recv string) bool {
	return scope[root.Name] || (recv != "" && root.Name == recv)
}

func paramOrRecvRule(root *ast.Ident, scope map[string]bool, recv string) string {
	if recv != "" && root.Name == recv {
		return "receiver:" + root.Name
	}
	return "param:" + root.Name
}
