package rename

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// RenameMethod renames a method ("Type.Method" or "pkg.Type.Method"). It is
// all-or-nothing: the method definition and every `.Method` reference whose
// receiver can be proven to be of type "Type" are renamed; any reference
// whose receiver type cannot be proven refuses the entire rename, listing the
// offending file:line. A reference provably on a different type is skipped,
// not refused, so shared method names (Save, Close, String) do not block the
// rename. Inline call receivers (s := store.New(); s.Save(), or
// store.New().Save()) are proven when the callee's declared return type is in
// the index; genuinely unprovable chains (s := helper(); s.Save()) are
// refused rather than guessed.
func RenameMethod(ix *index.Index, oldName, newName string, r *Report) (*Report, error) {
	if !token.IsIdentifier(newName) || token.Lookup(newName).IsKeyword() {
		return nil, fmt.Errorf("%q is not a valid Go identifier", newName)
	}
	if oldName == newName {
		return nil, fmt.Errorf("new name equals old name")
	}
	typeName, methodName, ok := splitMethodName(oldName)
	if !ok {
		return nil, &ErrNotSupported{Reason: "method renames take the form \"Type.Method\" (optionally \"pkg.Type.Method\"); package-level symbols use a bare name"}
	}

	// Locate the definition(s): method symbols with this receiver type + name.
	for _, s := range ix.Symbols {
		if s.Lang != "go" || s.Receiver != typeName || s.Name != methodName {
			continue
		}
		r.Defs = append(r.Defs, Loc{File: s.File, Line: s.Line, Col: 1})
	}
	sort.Slice(r.Defs, func(i, j int) bool {
		if r.Defs[i].File != r.Defs[j].File {
			return r.Defs[i].File < r.Defs[j].File
		}
		return r.Defs[i].Line < r.Defs[j].Line
	})
	if len(r.Defs) == 0 {
		return nil, fmt.Errorf("method %q not found in the index (run kern index first)", oldName)
	}

	var goFiles []string
	{
		uniq := map[string]bool{}
		for _, s := range ix.Symbols {
			if s.Lang == "go" && !uniq[s.File] {
				uniq[s.File] = true
				goFiles = append(goFiles, s.File)
			}
		}
		sort.Strings(goFiles)
	}

	fileSeen := map[string]bool{}
	for _, rel := range goFiles {
		edits, unproven := renameMethodFile(ix, filepath.Join(ix.Root, rel), typeName, methodName, newName)
		if len(unproven) > 0 {
			return nil, fmt.Errorf("method rename %q -> %q refused: %d reference(s) have an unprovable receiver type; every call in the project must be provably on a %s receiver (annotate the variable, rename the type, or use an editor):\n  %s",
				oldName, newName, len(unproven), typeName, strings.Join(prefixLines(unproven, 5), "\n  "))
		}
		if len(edits) == 0 {
			continue
		}
		r.Edits = append(r.Edits, edits...)
		if !fileSeen[rel] {
			fileSeen[rel] = true
			r.Files = append(r.Files, rel)
		}
	}
	sort.Slice(r.Edits, func(i, j int) bool {
		if r.Edits[i].File != r.Edits[j].File {
			return r.Edits[i].File < r.Edits[j].File
		}
		return r.Edits[i].Offset < r.Edits[j].Offset
	})
	sort.Strings(r.Files)
	if len(r.Edits) == 0 {
		r.NotEdited = true
	}
	return r, nil
}

// splitMethodName splits "pkg.Type.Method" or "Type.Method" into the receiver
// type name and the method name (any package prefix is dropped).
func splitMethodName(name string) (typeName, methodName string, ok bool) {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	typeName = parts[len(parts)-2]
	methodName = parts[len(parts)-1]
	if typeName == "" || methodName == "" || !token.IsIdentifier(typeName) {
		return "", "", false
	}
	return typeName, methodName, true
}

// renameMethodFile parses one Go file, returning its edits and the list of
// "file:line" references whose receiver type could not be proven.
func renameMethodFile(ix *index.Index, abs, typeName, methodName, newName string) ([]Edit, []string) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil // vanished since indexing — skip quietly
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, src, 0)
	if err != nil {
		return nil, nil // never-compiling file — leave it alone
	}

	rel, _ := filepath.Rel(ix.Root, abs)
	parents := parentMap(f)
	proof := newTypeProof(ix, f, rel)

	var edits []Edit
	var unproven []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			if v.Name == nil || v.Name.Name != methodName || v.Recv == nil {
				return true
			}
			for _, field := range v.Recv.List {
				if typeExprName(field.Type) != typeName {
					continue
				}
				pos := fset.Position(v.Name.Pos())
				edits = append(edits, Edit{
					File: abs, Line: pos.Line, Col: pos.Column, Offset: pos.Offset,
					Old: methodName, New: newName, Kind: "definition",
				})
				break
			}
		case *ast.SelectorExpr:
			if v.Sel == nil || v.Sel.Name != methodName {
				return true
			}
			rt := proof.receiverType(fnContaining(parents, v.X), v.X)
			if rt == typeName {
				// Provably on typeName — rename this reference.
				pos := fset.Position(v.Sel.Pos())
				edits = append(edits, Edit{
					File: abs, Line: pos.Line, Col: pos.Column, Offset: pos.Offset,
					Old: methodName, New: newName, Kind: "reference",
				})
				return true
			}
			if rt != "" {
				// Provably a DIFFERENT type — not a reference to this method; skip.
				return true
			}
			// Genuinely unprovable — refuse the whole rename.
			unproven = append(unproven, fmt.Sprintf("%s:%d", abs, fset.Position(v.Sel.Pos()).Line))
		}
		return true
	})
	return edits, unproven
}

// fnContaining walks up the parent chain to find the FuncDecl enclosing node,
// or nil when node is not inside a function (package level).
func fnContaining(parents map[ast.Node]ast.Node, node ast.Node) *ast.FuncDecl {
	for n := node; n != nil; n = parents[n] {
		if fd, ok := n.(*ast.FuncDecl); ok {
			return fd
		}
	}
	return nil
}

// literalReceiverType returns the type name when expr is a direct
// construction of it: Type{...}, (&Type{}).M, new(Type), Type(raw), or a
// parenthesized/pointer-wrapped form. The result is non-empty only for
// receiver expressions whose type is written down literally in the source.
func literalReceiverType(expr ast.Expr) string {
	for i := 0; i < 4; i++ {
		switch v := expr.(type) {
		case *ast.ParenExpr:
			expr = v.X
		case *ast.StarExpr:
			expr = v.X
		case *ast.UnaryExpr: // &Type{...} wraps the construction
			if cl, ok := v.X.(*ast.CompositeLit); ok {
				return typeExprName(cl.Type)
			}
			expr = v.X
		default:
			goto classify
		}
	}
classify:
	switch v := expr.(type) {
	case *ast.CompositeLit:
		return typeExprName(v.Type)
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
			return typeExprName(v.Args[0])
		}
		if t := typeExprName(v.Fun); t != "" && t != "new" {
			return t // Type(raw) conversion as receiver
		}
	case *ast.Ident:
		return v.Name
	}
	return ""
}

// newTypeProof builds the variable→type proof for a file. It carries the
// index so a binding like `s := store.New()` can be proven from the callee's
// declared return types (ix.Symbol.Returns) — the receiver chain the v1
// rename refused as unprovable.
func newTypeProof(ix *index.Index, f *ast.File, file string) *typeProof {
	tp := &typeProof{
		ix:      ix,
		pkg:     map[string]string{},
		fn:      map[*ast.FuncDecl]map[string]string{},
		imports: map[string]string{},
		file:    file,
	}
	// Build qualifier -> import path map so qualified callees (store.New) can
	// be disambiguated when multiple packages export a same-named function.
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			tp.imports[imp.Name.Name] = path // aliased import
		} else {
			// Unaliased: the qualifier is the package name = last path segment.
			qual := path
			if i := strings.LastIndex(qual, "/"); i >= 0 {
				qual = qual[i+1:]
			}
			tp.imports[qual] = path
		}
	}
	// package-level vars with explicit types
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			t := typeExprName(vs.Type)
			if t == "" {
				continue
			}
			for _, id := range vs.Names {
				mergeType(tp.pkg, id.Name, t)
			}
		}
	}
	// per-function locals + receivers + := and = literals
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		locals := map[string]string{}
		if fd.Recv != nil {
			for _, field := range fd.Recv.List {
				if t := typeExprName(field.Type); t != "" {
					for _, id := range field.Names {
						mergeType(locals, id.Name, t)
					}
				}
			}
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || i >= len(v.Rhs) {
						continue
					}
					if t := rhsType(v.Rhs[i], ix, tp); t != "" {
						mergeType(locals, id.Name, t)
					}
				}
			case *ast.ValueSpec:
				if v.Type != nil {
					if t := typeExprName(v.Type); t != "" {
						for _, id := range v.Names {
							mergeType(locals, id.Name, t)
						}
					}
					return true
				}
				for i, val := range v.Values {
					if t := rhsType(val, ix, tp); t != "" && i < len(v.Names) {
						mergeType(locals, v.Names[i].Name, t)
					}
				}
			}
			return true
		})
		tp.fn[fd] = locals
	}
	return tp
}

// typeProof proves variable→type bindings per function (and package-level).
// A name bound to more than one type becomes unprovable ("").
type typeProof struct {
	ix      *index.Index
	pkg     map[string]string
	fn      map[*ast.FuncDecl]map[string]string
	imports map[string]string // qualifier/alias -> import path
	file    string            // calling file (relative to ix.Root)
}

// receiverType returns the proven type name of expr in fd's scope, or "" when
// it cannot be proven. A non-empty result that differs from the rename target
// typeName means the reference is provably on a different type and must be
// SKIPPED, not refused — this is what lets two types share a method name
// (Save, Close, String) without breaking the rename. nil fd = package level.
//
// Resolution order:
//  1. Variable receiver (a.M) — scope binding from typeProof. Checked FIRST
//     because literalReceiverType returns the bare ident name for an Ident,
//     which is a variable name, not a type.
//  2. Inline call receiver (f().M, new(T).M, T(raw).M) — callee return type
//     from the index, or the type argument for new(), or the fun name for a
//     conversion.
//  3. Literal construction (&Type{}, Type{}) — type written in source.
func (tp *typeProof) receiverType(fd *ast.FuncDecl, expr ast.Expr) string {
	// Variable receiver: a.M — resolve from the scope binding.
	if id, ok := expr.(*ast.Ident); ok {
		if l, ok := tp.fn[fd]; ok {
			if t, ok := l[id.Name]; ok {
				return t // "" means ambiguous (mergeType marked it)
			}
		}
		if t, ok := tp.pkg[id.Name]; ok {
			return t
		}
		// Unknown ident — not a known variable. Fall through in case it's a
		// type name used literally (rare; literalReceiverType handles it).
	}
	// Inline call receiver — handled BEFORE literalReceiverType, which returns
	// the callee name for a generic call rather than the return type.
	if ce, ok := expr.(*ast.CallExpr); ok {
		if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "new" && len(ce.Args) == 1 {
			return typeExprName(ce.Args[0])
		}
		if t := tp.callReturnsType(ce.Fun); t != "" {
			return t
		}
		// Type conversion: T(raw).M — fun is a type name.
		if t := typeExprName(ce.Fun); t != "" && t != "new" {
			return t
		}
		return ""
	}
	// Literal construction: &Type{}, Type{...}.M — type written in source.
	if t := literalReceiverType(expr); t != "" {
		return t
	}
	return ""
}

// mergeType records name→type or marks it ambiguous when the existing
// binding differs (ambiguity never proves anything — it only refuses).
func mergeType(m map[string]string, name, t string) {
	if cur, ok := m[name]; ok {
		if cur != t {
			m[name] = ""
		}
		return
	}
	m[name] = t
}

// rhsType returns the type name a value binds to when the syntax proves it:
// &Type{...}, Type{...}, new(Type), or any call whose callee's declared
// return type is in the index (returns-of). Returns "" when not provable.
// tp is the per-file type proof (carries the import map for disambiguation);
// nil falls back to the legacy first-match lookup.
func rhsType(e ast.Expr, ix *index.Index, tp *typeProof) string {
	switch v := e.(type) {
	case *ast.CompositeLit:
		return typeExprName(v.Type)
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
			return typeExprName(v.Args[0])
		}
		if tp != nil {
			return tp.callReturnsType(v.Fun)
		}
		return callReturnsType(ix, v.Fun)
	case *ast.UnaryExpr: // &T{...}
		if cl, ok := v.X.(*ast.CompositeLit); ok {
			return typeExprName(cl.Type)
		}
	}
	return ""
}

// callReturnsType is the legacy non-method variant (no import context) used
// by callers that lack a typeProof. It returns the first match's return type
// when there is exactly one candidate, "" otherwise. Prefer
// (*typeProof).callReturnsType for disambiguation when a file's imports are
// available.
func callReturnsType(ix *index.Index, fun ast.Expr) string {
	var name string
	switch v := fun.(type) {
	case *ast.SelectorExpr:
		name = v.Sel.Name
	case *ast.Ident:
		name = v.Name
	}
	if name == "" {
		return ""
	}
	var hit string
	for _, s := range ix.Symbols {
		if s.Lang != "go" || s.Name != name || len(s.Returns) == 0 {
			continue
		}
		if hit != "" && hit != s.Returns[0] {
			return "" // multiple candidates with different returns — ambiguous
		}
		hit = s.Returns[0]
	}
	return hit
}

// callReturnsType resolves the declared first return type of a callee
// expression against the index, e.g. `store.New` -> "Store" when the symbol's
// Returns contains it. "" when unknown.
//
// Disambiguation: when multiple symbols share the callee name (e.g. two
// packages both export `New()`), the qualifier is matched against the file's
// import map to find the import path, and the candidate whose directory is
// the last segment of that path wins. For unqualified local calls, the
// candidate in the same directory as the calling file wins. If no candidate
// is uniquely resolvable, "" is returned (refuse — never guess).
func (tp *typeProof) callReturnsType(fun ast.Expr) string {
	var qual, name string
	switch v := fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			qual = id.Name // package qualifier (store in store.New)
		}
		name = v.Sel.Name
	case *ast.Ident:
		name = v.Name
	}
	if name == "" {
		return ""
	}

	// Collect every indexed symbol matching this callee name with a return type.
	var matches []index.Symbol
	for _, s := range tp.ix.Symbols {
		if s.Lang != "go" || s.Name != name || len(s.Returns) == 0 {
			continue
		}
		matches = append(matches, s)
	}
	if len(matches) == 0 {
		return ""
	}
	if len(matches) == 1 {
		return matches[0].Returns[0]
	}

	// Multiple candidates — disambiguate by qualifier/directory.
	if qual != "" {
		// Qualified call (store.New): map qualifier to import path, then match
		// the candidate whose directory is the last path segment of the import.
		impPath, ok := tp.imports[qual]
		if !ok {
			return "" // unknown import — refuse
		}
		wantDir := impPath
		if i := strings.LastIndex(wantDir, "/"); i >= 0 {
			wantDir = wantDir[i+1:]
		}
		var hit string
		for _, s := range matches {
			if filepath.Dir(s.File) == wantDir {
				if hit != "" && hit != s.Returns[0] {
					return "" // two candidates in same dir, different returns — ambiguous
				}
				hit = s.Returns[0]
			}
		}
		return hit
	}
	// Unqualified local call (New): same-directory candidate wins.
	wantDir := filepath.Dir(tp.file)
	var hit string
	for _, s := range matches {
		if filepath.Dir(s.File) == wantDir {
			if hit != "" && hit != s.Returns[0] {
				return "" // ambiguous within the same package
			}
			hit = s.Returns[0]
		}
	}
	return hit
}

// typeExprName returns the base identifier of a possibly-qualified, pointer
// or generic type expression: *pkg.Type / Type / Type[A] -> "Type".
func typeExprName(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return typeExprName(v.X)
	case *ast.IndexExpr:
		return typeExprName(v.X)
	case *ast.IndexListExpr:
		return typeExprName(v.X)
	case *ast.ParenExpr:
		return typeExprName(v.X)
	case *ast.SelectorExpr:
		return typeExprName(v.Sel)
	case *ast.ArrayType:
		return ""
	}
	return ""
}

// prefixLines bounds a list for the refusal message.
func prefixLines(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	out := append([]string{}, ss[:n]...)
	return append(out, fmt.Sprintf("(and %d more)", len(ss)-n))
}
