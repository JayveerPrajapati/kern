package intel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// nonCallRefIndex records, per bare identifier name, which indexed files
// reference that name outside a call position. Call positions (the callee of a
// *ast.CallExpr) are already tracked by the index's call graph; everything else
// — a function value (`f := Foo; f()`), a method value (`h := receiver.Method`),
// a function passed as an argument, a struct/interface member name, a bare
// comparison — is invisible to the call graph. This pass makes DeleteCheck and
// DeadCode aware of those references so they never report genuinely-used code
// as dead or safe to delete.
// It is built at query time (not persisted into the index cache) and parses each
// indexed Go file exactly once.
type nonCallRefIndex struct {
	byName map[string]map[string]bool // bare name -> set of rel file paths
}

// newNonCallRefIndex builds a non-call reference index for every Go file in the
// project index. Callers that only need one symbol pay the same parse cost as a
// full scan, but each file is parsed exactly once regardless.
func newNonCallRefIndex(ix *index.Index) *nonCallRefIndex {
	ri := &nonCallRefIndex{byName: map[string]map[string]bool{}}
	if ix == nil {
		return ri
	}
	for rel := range ix.FileHashes {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(ix.Root, rel))
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
		if err != nil {
			continue
		}
		collectNonCallRefs(f, ri.byName, rel)
	}
	return ri
}

// collectNonCallRefs records, in byName, every bare identifier in f that is a
// non-call, non-declaration reference. rel is the file path recorded on the
// entries.
func collectNonCallRefs(f *ast.File, byName map[string]map[string]bool, rel string) {
	skip := nonCallSkipSet(f)
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || skip[id] {
			return true
		}
		if byName[id.Name] == nil {
			byName[id.Name] = map[string]bool{}
		}
		byName[id.Name][rel] = true
		return true
	})
}

// nonCallSkipSet returns the identifiers in f that must NOT be treated as
// non-call references: (1) the callee identifiers of any *ast.CallExpr, which
// the index already records as call edges, and (2) declaration names (a
// function/type/var/const/field's own name), which are definitions, not uses.
// Without excluding declaration names, every symbol would appear "referenced"
// by its own definition.
func nonCallSkipSet(f *ast.File) map[*ast.Ident]bool {
	skip := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.CallExpr:
			// The callee expression is the call position; the index already
			// records it. Skip its identifiers (both the fun name and the
			// receiver qualifier, e.g. both `app` and `Run` in `app.Run()`).
			ast.Inspect(t.Fun, func(x ast.Node) bool {
				if id, ok := x.(*ast.Ident); ok {
					skip[id] = true
				}
				return true
			})
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
			// Struct fields, interface methods, function params/results and
			// receiver names are all declarations, not uses.
			for _, nm := range t.Names {
				skip[nm] = true
			}
		}
		return true
	})
	return skip
}

// productionFiles returns the indexed, non-test files that reference name
// outside a call position. Only non-test files count: a test-only reference
// (like a test-only call) is not enough to keep a symbol out of dead-code or
// the safe-to-delete set, because deleting the tests is part of the change.
func (ri *nonCallRefIndex) productionFiles(name string) []string {
	var out []string
	for f := range ri.byName[name] {
		if !isTestFile(f) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// referencedOutsideCall reports whether name appears as a non-call reference in
// any production (non-test) indexed file.
func (r *nonCallRefIndex) referencedOutsideCall(name string) bool {
	return len(r.productionFiles(name)) > 0
}

// files returns every indexed file (test or not) that references name outside a
// call position.
func (r *nonCallRefIndex) files(name string) []string {
	var out []string
	for f := range r.byName[name] {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
