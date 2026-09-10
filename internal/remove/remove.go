// Package remove plans and applies safe source-level symbol deletion. The
// plan is fail-closed: it refuses unless the deletion gate (intel.DeleteCheck)
// says Safe, the symbol is a function or method, and no test file references
// the name outside a call position. The applied edits remove the symbol's
// declaration and the declarations of its test-only callers (the gate's
// contract: "remove the tests together"), through rename.Apply so every edit
// is byte-verified and all files are backed up with rollback on failure.
package remove

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
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/rename"
)

// ErrUnsafe is returned when the deletion gate does not sanction the removal.
// Reason carries the gate's own explanation.
type ErrUnsafe struct{ Reason string }

func (e *ErrUnsafe) Error() string { return "not safe to delete: " + e.Reason }

// ErrUnsupported is returned when the symbol's shape cannot be removed safely
// (only functions and methods can — a type's methods and a var's initializers
// are invisible to the call graph).
type ErrUnsupported struct{ Reason string }

func (e *ErrUnsupported) Error() string { return "delete not supported: " + e.Reason }

// Plan computes the deletion plan for sym: the exact byte-range edits that
// remove the symbol's declaration and every test-only caller's declaration.
// It refuses with a typed error when the gate is not satisfied. The returned
// report is ready for rename.Apply, which backs up all touched files and
// rolls back on any failure.
func Plan(ix *index.Index, sym string) (*rename.Report, error) {
	if ix == nil || sym == "" {
		return nil, fmt.Errorf("remove: no index or empty symbol")
	}
	check := intel.DeleteCheck(ix, sym)
	if !check.Defined {
		return nil, fmt.Errorf("remove: %s", check.Reason)
	}
	// Resolve qualified input ("app.TaskService.Deploy") to the canonical
	// name, exactly like the gate does.
	if resolved, ok := intel.Resolve(ix, sym); ok && resolved != sym {
		sym = resolved
	}
	var target *index.Symbol
	for _, d := range ix.Search(sym, 50) {
		if d.FullName() == sym {
			if d.Kind != "func" && d.Kind != "method" {
				return nil, &ErrUnsupported{fmt.Sprintf("%s is a %s; apply supports functions and methods only", sym, d.Kind)}
			}
			d := d
			target = &d
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("remove: symbol %q not found in the index", sym)
	}
	// The call-graph gate comes after the shape check: for a type or var the
	// gate's verdict is meaningless (the call graph cannot see type/var
	// dependencies), so the shape error is the honest one.
	if !check.Safe {
		return nil, &ErrUnsafe{check.Reason}
	}
	// Test files must reference the name ONLY through calls from test
	// functions (which the plan removes together with the symbol). Any
	// non-call reference would survive the edit set and break the build.
	if files := testNonCallRefs(ix, simpleName(sym)); len(files) > 0 {
		return nil, &ErrUnsafe{fmt.Sprintf("test files reference %s outside a call (function/method value, assignment, etc.): %s", simpleName(sym), strings.Join(files, ", "))}
	}
	// Removal set: the symbol's declaration plus every test-only caller's
	// declaration (DeleteCheck splits callers into production/test; Safe
	// means production callers are absent).
	removals := []declRange{{file: target.File, start: target.Line, end: target.End}}
	for _, caller := range check.TestCallers {
		if d := findFunc(ix, caller); d != nil {
			removals = append(removals, declRange{file: d.File, start: d.Line, end: d.End})
		}
	}
	edits, err := buildEdits(ix.Root, removals)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("remove: no edits computed for %s", sym)
	}
	files := make([]string, 0, len(edits))
	seen := map[string]bool{}
	for _, e := range edits {
		if !seen[e.File] {
			seen[e.File] = true
			files = append(files, e.File)
		}
	}
	sort.Strings(files)
	return &rename.Report{Symbol: sym, Edits: edits, Files: files}, nil
}

// declRange is a declaration to remove: the file (relative to the index root)
// and the 1-based first and last lines of the declaration body.
type declRange struct {
	file  string
	start int
	end   int
}

// findFunc resolves a caller name to its declaration when it is a function or
// method defined in a test file.
func findFunc(ix *index.Index, name string) *index.Symbol {
	for _, d := range ix.Search(name, 50) {
		if (d.Kind == "func" || d.Kind == "method") && strings.HasSuffix(d.File, "_test.go") {
			if d.FullName() == name || d.Name == name {
				d := d
				return &d
			}
		}
	}
	return nil
}

// simpleName returns the segment after the last dot ("" for a plain name).
func simpleName(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// testNonCallRefs lists test files that reference name outside a call
// position or declaration — the one reference shape the plan cannot remove.
func testNonCallRefs(ix *index.Index, name string) []string {
	var out []string
	for rel := range ix.FileHashes {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		abs := filepath.Join(ix.Root, rel)
		src, err := os.ReadFile(abs)
		if err != nil || !strings.Contains(string(src), name) {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
		if err != nil {
			continue
		}
		if testRefsName(f, name) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// testRefsName reports whether f references name outside a call position and
// outside a declaration. Call callees are excluded (the plan removes the
// calling test function wholesale), as are declaration names (definitions,
// not uses).
func testRefsName(f *ast.File, name string) bool {
	skip := nonCallSkipSet(f)
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok || skip[id] || id.Name != name {
			return true
		}
		found = true
		return false
	})
	return found
}

// nonCallSkipSet returns the identifiers of f that must not count as
// references: callee identifiers of CallExprs (already call edges) and
// declaration names (a symbol's own definition).
func nonCallSkipSet(f *ast.File) map[*ast.Ident]bool {
	skip := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.CallExpr:
			ast.Inspect(t.Fun, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
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
			for _, id := range t.Names {
				skip[id] = true
			}
		case *ast.Field:
			for _, id := range t.Names {
				skip[id] = true
			}
		}
		return true
	})
	return skip
}

// buildEdits converts removal ranges into byte-offset rename.Edits. Each
// range is extended upward over contiguous doc-comment and blank lines and
// downward over one trailing blank line, so the file stays gofmt-clean.
// Edits carry ABSOLUTE file paths (rename.Apply reads them directly and
// derives relative backups from the root).
func buildEdits(root string, removals []declRange) ([]rename.Edit, error) {
	byFile := map[string][]declRange{}
	for _, r := range removals {
		byFile[r.file] = append(byFile[r.file], r)
	}
	var edits []rename.Edit
	for rel, ranges := range byFile {
		abs := filepath.Join(root, rel)
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(src), "\n")
		starts := lineStarts(src)
		for _, r := range ranges {
			s, e := extendRange(lines, r.start, r.end)
			if s < 1 || e > len(lines) || s > e {
				return nil, fmt.Errorf("remove: invalid removal range %d..%d in %s", s, e, rel)
			}
			startOff := starts[s-1]
			endOff := len(src)
			if e < len(lines) {
				endOff = starts[e]
			}
			if endOff <= startOff {
				return nil, fmt.Errorf("remove: empty removal range in %s", rel)
			}
			edits = append(edits, rename.Edit{
				File:   abs,
				Offset: startOff,
				Old:    string(src[startOff:endOff]),
				New:    "",
				Kind:   "definition",
			})
		}
	}
	return edits, nil
}

// lineStarts maps each 1-based line number to its byte offset in src.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// extendRange widens [start,end] (1-based, inclusive) over contiguous
// doc-comment and blank lines above, and one trailing blank line below.
func extendRange(lines []string, start, end int) (int, int) {
	s := start
	for s > 1 {
		t := strings.TrimSpace(lines[s-2])
		if t == "" || strings.HasPrefix(t, "//") {
			s--
			continue
		}
		break
	}
	e := end
	if e < len(lines) && strings.TrimSpace(lines[e-1]) == "" {
		e++
	}
	return s, e
}
