// Package refscan finds references the call graph cannot see.
//
// The call graph records call positions only. A function value, a method
// value, a type used in a declaration, a const, or a var has no call edge, so
// "0 callers" for those is absence of coverage — never evidence that nothing
// uses them. This package provides the two reference passes that close that
// gap: an AST non-call reference index (Go) for dead-code/delete safety, and a
// bounded word-boundary textual scan for the honest blast-radius safety net
// (audit P0-1). It was extracted from internal/intel when that subsystem hit
// its ARCHITECTURE.md cap; the extraction is the contract-mandated response.
package refscan

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// NonCallIndex records, per bare identifier name, which indexed files
// reference that name outside a call position. Call positions (the callee of a
// *ast.CallExpr) are already tracked by the index's call graph; everything else
// — a function value (`f := Foo; f()`), a method value (`h := receiver.Method`),
// a function passed as an argument, a struct/interface member name, a bare
// comparison — is invisible to the call graph. This pass makes DeleteCheck and
// DeadCode aware of those references so they never report genuinely-used code
// as dead or safe to delete.
// It is built at query time (not persisted into the index cache) and parses each
// indexed Go file exactly once.
type NonCallIndex struct {
	byName map[string]map[string]bool // bare name -> set of rel file paths
}

// NewNonCallIndex builds a non-call reference index for every Go file in the
// project index. Callers that only need one symbol pay the same parse cost as a
// full scan, but each file is parsed exactly once regardless.
func NewNonCallIndex(ix *index.Index) *NonCallIndex {
	ri := &NonCallIndex{byName: map[string]map[string]bool{}}
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

// ProductionFiles returns the indexed, non-test files that reference name
// outside a call position. Only non-test files count: a test-only reference
// (like a test-only call) is not enough to keep a symbol out of dead-code or
// the safe-to-delete set, because deleting the tests is part of the change.
func (r *NonCallIndex) ProductionFiles(name string) []string {
	var out []string
	for f := range r.byName[name] {
		if !code.IsTestFile(f) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// ReferencedOutsideCall reports whether name appears as a non-call reference
// in any production (non-test) indexed file.
func (r *NonCallIndex) ReferencedOutsideCall(name string) bool {
	return len(r.ProductionFiles(name)) > 0
}

// textualRefScanBudget caps the total bytes TextualReferences reads so a very
// large checkout cannot turn an impact query into an unbounded I/O pass.
const textualRefScanBudget = 64 << 20

// ReferenceOnlyKind reports symbol kinds whose uses are invisible to the call
// graph: types used only in declarations, consts, vars, fields. For these
// kinds "0 callers" is absence of coverage, never evidence of no impact.
func ReferenceOnlyKind(kind string) bool {
	switch kind {
	case "struct", "interface", "class", "type", "enum", "record", "const", "var", "field":
		return true
	}
	return false
}

// TextualReferences counts word-boundary occurrences of name across the
// indexed source files and returns the distinct referencing files (sorted),
// the occurrence total, and whether the scan completed within
// textualRefScanBudget.
//
// It is the honesty safety net for symbols the call graph cannot see: a type
// used only in declarations, a const, a var, or an interface has no call edge,
// so the graph's "0 callers" is absence of coverage, not evidence of no
// impact. Confidence is TEXTUAL by construction — comments, strings, and
// same-named identifiers from other packages count — so callers must label it
// as such and must never treat it as a resolved reference set. excludeFile
// (usually the target's definition file) is skipped so a symbol is not
// "referenced" by its own declaration. Generated files are skipped.
func TextualReferences(ix *index.Index, name, excludeFile string) (files []string, occurrences int, complete bool) {
	return textualReferences(ix, name, excludeFile, 0, false)
}

// TextualReferencesIncludingDecl is TextualReferences that also scans the
// definition file, subtracting occurrences on defLine (the declaration is not
// a reference). Same-file uses — `new Widget(1)` in the file that declares
// Widget — are real references; excluding the whole defining file made
// single-file non-Go projects report zero references.
func TextualReferencesIncludingDecl(ix *index.Index, name, defFile string, defLine int) (files []string, occurrences int, complete bool) {
	return textualReferences(ix, name, defFile, defLine, true)
}

func textualReferences(ix *index.Index, name, defFile string, defLine int, includeDef bool) (files []string, occurrences int, complete bool) {
	if ix == nil || name == "" {
		return nil, 0, true
	}
	needle := []byte(name)
	scanned := 0
	for rel := range ix.FileHashes {
		if !includeDef && rel == defFile {
			continue
		}
		if ix.GeneratedFiles[rel] || !isSourcePath(rel) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(ix.Root, rel))
		if err != nil {
			continue
		}
		if scanned+len(src) > textualRefScanBudget {
			return files, occurrences, false
		}
		scanned += len(src)
		n := CountWordBoundary(src, needle)
		if includeDef && rel == defFile {
			// Declarations are not uses. Subtract every declaration line in
			// this file that carries the same name (the class and its
			// constructor share it), plus the resolved defLine when the index
			// did not record SymbolsByFile. A stale/EOF line subtracts nothing.
			lines := map[int]bool{}
			if defLine > 0 {
				lines[defLine] = true
			}
			for _, sym := range ix.SymbolsByFile[defFile] {
				if sym.Name == name && sym.Line > 0 {
					lines[sym.Line] = true
				}
			}
			for line := range lines {
				n -= countWordBoundaryOnLine(src, needle, line)
			}
		}
		if n > 0 {
			files = append(files, rel)
			occurrences += n
		}
	}
	sort.Strings(files)
	return files, occurrences, true
}

// countWordBoundaryOnLine counts matches confined to a single 1-based line.
func countWordBoundaryOnLine(src, needle []byte, line int) int {
	if line < 1 || len(needle) == 0 {
		return 0
	}
	start, cur := 0, 1
	for cur < line {
		i := bytes.IndexByte(src[start:], '\n')
		if i < 0 {
			return 0
		}
		start += i + 1
		cur++
	}
	end := len(src)
	if i := bytes.IndexByte(src[start:], '\n'); i >= 0 {
		end = start + i
	}
	return CountWordBoundary(src[start:end], needle)
}

// CountWordBoundary counts non-overlapping occurrences of needle in src whose
// neighbours are not identifier characters (ASCII: letters, digits, '_').
func CountWordBoundary(src, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	n := 0
	for i := 0; ; {
		j := bytes.Index(src[i:], needle)
		if j < 0 {
			break
		}
		j += i
		beforeOK := j == 0 || !isIdentByte(src[j-1])
		after := j + len(needle)
		afterOK := after == len(src) || !isIdentByte(src[after])
		if beforeOK && afterOK {
			n++
			i = after
		} else {
			i = j + 1
		}
	}
	return n
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// isSourcePath limits the textual scan to source-code files. The index also
// tracks prose/heading symbols from Markdown and config files; counting an
// English word like "risk" in a README would inflate an identifier's textual
// blast radius without adding a real reference.
func isSourcePath(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".py", ".java", ".kt", ".kts", ".rs", ".rb", ".php", ".cs",
		".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".swift", ".scala",
		".sh", ".bash", ".lua", ".ex", ".exs", ".hs", ".dart",
		".vue", ".svelte", ".sql", ".proto":
		return true
	}
	return false
}
