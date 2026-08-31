// Package rename implements structural symbol renaming on top of the AST index.
// It supports package-level Go symbols and method rename ("Type.Method") when
// every reference's receiver type can be proven from AST evidence in the same
// file; unprovable references are refused, never guessed, and references
// provably on a different type are skipped so shared method names do not block
// the rename. Edits come from a real go/ast parse (never a textual regex), so
// strings, comments, struct-field names, composite-literal keys, import aliases
// and the package clause are never touched. Applying is transactional: every
// touched file is backed up before any write, and a mid-flight failure restores
// all files.
package rename

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Loc is one definition site of the renamed symbol.
type Loc struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// Edit is a single identifier replacement in a source file.
type Edit struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Offset int    `json:"-"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Kind   string `json:"kind"` // definition | reference
}

// Report is the full result of a rename: what would change, and what was
// deliberately left alone.
type Report struct {
	Symbol    string   `json:"symbol"`
	NewName   string   `json:"new_name"`
	Defs      []Loc    `json:"defs"`
	Edits     []Edit   `json:"edits"`
	Skipped   []string `json:"skipped,omitempty"` // references deliberately not edited
	Files     []string `json:"files,omitempty"`
	Applied   bool     `json:"applied"`
	Backup    string   `json:"backup,omitempty"`
	NotEdited bool     `json:"not_edited"` // symbol found but had no editable references
}

// ErrNotSupported is returned when the rename is unsupported (method-receiver
// issues, non-Go symbols) so callers can distinguish "not found" from "refused".
type ErrNotSupported struct{ Reason string }

func (e *ErrNotSupported) Error() string { return "rename not supported: " + e.Reason }

// Rename computes every edit needed to rename oldName to newName across the
// indexed project. It never touches the filesystem; call Apply to commit.
// oldName is either a package-level Go symbol ("Adder") or a method
// ("Type.Method"). A method rename is all-or-nothing: it requires every
// .Method reference in the project to be provably on a receiver typed as
// "Type" (var t *Type, t := &Type{...}, new(Type), Type{...}, or a constructor
// call whose indexed return type is "Type"); a reference provably on a
// different type is skipped, and any reference whose receiver type cannot be
// proven refuses the whole rename so the tree is never left compiling against
// a half-renamed method. Call chains whose return type is not in the index
// (s := localHelper(); s.Save()) are refused rather than guessed — annotate
// the variable or rename package-level symbols instead.
func Rename(ix *index.Index, oldName, newName string) (*Report, error) {
	r := &Report{Symbol: oldName, NewName: newName}
	if ix == nil {
		return nil, fmt.Errorf("no index")
	}
	if oldName == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	if strings.Contains(oldName, ".") {
		return RenameMethod(ix, oldName, newName, r)
	}
	if !token.IsIdentifier(newName) || token.Lookup(newName).IsKeyword() {
		return nil, fmt.Errorf("%q is not a valid Go identifier", newName)
	}
	if oldName == newName {
		return nil, fmt.Errorf("new name equals old name")
	}

	// Definitions: the symbol must exist and be a Go symbol.
	for _, d := range ix.Search(oldName, 200) {
		if d.Name != oldName {
			continue
		}
		if d.Lang != "go" {
			return nil, &ErrNotSupported{Reason: "symbol " + oldName + " is defined in a non-Go file (" + d.File + "); v1 renames Go symbols only"}
		}
		r.Defs = append(r.Defs, Loc{File: d.File, Line: d.Line, Col: 1})
	}
	if len(r.Defs) == 0 {
		return nil, fmt.Errorf("symbol %q not found in the index (run kern index first)", oldName)
	}

	// Symbol package (Dir of its definition file): needed to recognise
	// package-qualified references (pkg.Symbol) in other packages.
	symPkg := filepath.Dir(r.Defs[0].File)

	// Callers in non-Go files are reported as skipped, never edited.
	for _, c := range ix.Callers[oldName] {
		if f := callerFile(ix, c); f != "" {
			if lang := symbolLang(ix, f); lang != "go" {
				r.Skipped = append(r.Skipped, "caller "+c+" ("+f+", "+lang+") — non-Go heuristic rename not supported")
			}
		}
	}

	// Candidate files: every Go file in the index (symbols carry Lang). This is
	// a one-shot structural edit, so a full parse pass is acceptable.
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

	exported := isExported(oldName)
	fileSeen := map[string]bool{}
	for _, rel := range goFiles {
		edits := renameFile(ix, filepath.Join(ix.Root, rel), oldName, newName, symPkg, exported)
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
	sort.Strings(r.Skipped)
	if len(r.Edits) == 0 {
		r.NotEdited = true
	}
	return r, nil
}

// renameFile runs the AST pass over one Go file and returns its edits.
func renameFile(ix *index.Index, abs, oldName, newName, symPkg string, exported bool) []Edit {
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, src, 0)
	if err != nil {
		return nil
	}

	// Package-qualifier names for this file: which selector Xs refer to
	// imported packages, and whether that package is the renamed symbol's own
	// package (so pkg.Symbol is renamed, while bytes.Buffer never is).
	qualifiers := map[string]bool{}   // selector X name used as a package qualifier
	qualToSymPkg := map[string]bool{} // subset of qualifiers that import the symbol's package
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		ours := symPkg != "" && packageDirOf(ix, p) == symPkg
		declared := packageNameOf(ix, p)
		if declared == "" {
			declared = filepath.Base(p)
		}
		if imp.Name != nil {
			qualifiers[imp.Name.Name] = true
			if ours {
				qualToSymPkg[imp.Name.Name] = true
			}
			continue
		}
		qualifiers[declared] = true
		if ours {
			qualToSymPkg[declared] = true
		}
	}

	parents := parentMap(f)

	var edits []Edit
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != oldName {
			return true
		}
		kind, ok := classify(parents, f, id, qualifiers, qualToSymPkg, exported)
		if !ok {
			return true // excluded context
		}
		pos := fset.Position(id.Pos())
		edits = append(edits, Edit{
			File: abs, Line: pos.Line, Col: pos.Column, Offset: pos.Offset,
			Old: oldName, New: newName, Kind: kind,
		})
		return true
	})
	return edits
}

// parentMap records the AST parent of every node so occurrence context can be
// classified without re-walking. Correct parent tracking via a visitor that
// carries its parent down the traversal.
func parentMap(f *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	ast.Walk(parentV{parents: parents}, f)
	return parents
}

type parentV struct {
	parents map[ast.Node]ast.Node
	parent  ast.Node
}

func (p parentV) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}
	p.parents[n] = p.parent
	return parentV{parents: p.parents, parent: n}
}

// classify decides whether an identifier occurrence should be renamed and what
// kind it is. ok=false means the occurrence is an excluded context.
func classify(parents map[ast.Node]ast.Node, file *ast.File, id *ast.Ident, qualifiers, qualToSymPkg map[string]bool, exported bool) (string, bool) {
	p, ok := parents[id]
	if !ok {
		return "", false
	}
	switch par := p.(type) {
	case *ast.File:
		// Package clause (`package <name>`) is not a symbol reference.
		if par.Name == id {
			return "", false
		}
	case *ast.ImportSpec:
		return "", false // import alias
	case *ast.KeyValueExpr:
		if par.Key == id {
			return "", false // composite-literal field key
		}
	case *ast.SelectorExpr:
		if par.Sel == id {
			// Package-qualified reference to an exported symbol of its own
			// package, e.g. pkg.Symbol. Anything else (method call, field
			// access) is not renamed.
			if x, isID := par.X.(*ast.Ident); isID && qualifiers[x.Name] && qualToSymPkg[x.Name] && exported {
				return "reference", true
			}
			return "", false
		}
	case *ast.Field:
		for _, n := range par.Names {
			if n == id {
				return "", false // struct-field declaration name
			}
		}
	case *ast.LabeledStmt:
		if par.Label == id {
			return "", false // label, not a reference
		}
	case *ast.FuncDecl:
		if par.Name == id {
			return "definition", true
		}
	case *ast.TypeSpec:
		if par.Name == id {
			return "definition", true
		}
	case *ast.ValueSpec:
		for _, n := range par.Names {
			if n == id {
				return "definition", true
			}
		}
	}
	// Everything else — calls, type positions, selector X, params, return
	// types, composite-literal values — is a reference to the symbol.
	return "reference", true
}

// Apply commits the report's edits transactionally: every touched file is first
// copied under <root>/.kern/rename-backup/<timestamp>/, edits are applied by
// byte offset, and a mid-flight failure restores all files. It returns the
// number of edits applied.
func Apply(root string, r *Report) (int, error) {
	if r.Applied {
		return 0, nil
	}
	if len(r.Edits) == 0 {
		return 0, nil
	}
	// The index (and therefore every edit path) is rooted at an absolute path,
	// so canonicalize root the same way before computing relative backups.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	backupDir := filepath.Join(absRoot, ".kern", "rename-backup", fmt.Sprintf("%d", time.Now().UnixNano()))

	byFile := map[string][]Edit{}
	for _, e := range r.Edits {
		byFile[e.File] = append(byFile[e.File], e)
	}

	// Stage 1: back up every file we will touch. Failure here is safe — nothing
	// has been modified yet.
	backedUp := map[string]string{} // abs file -> backup path
	for abs := range byFile {
		src, err := os.ReadFile(abs)
		if err != nil {
			return 0, err
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil {
			return 0, err
		}
		bp := filepath.Join(backupDir, rel)
		if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(bp, src, 0o644); err != nil {
			return 0, err
		}
		backedUp[abs] = bp
	}

	// Stage 2: apply edits per file, highest offsets first. On any error,
	// restore every backed-up file so the tree is exactly as it was.
	applied := 0
	for abs, edits := range byFile {
		src, err := os.ReadFile(abs)
		if err != nil {
			restore(backedUp)
			return 0, err
		}
		next, err := splice(src, edits)
		if err != nil {
			restore(backedUp)
			return 0, err
		}
		if err := os.WriteFile(abs, next, 0o644); err != nil {
			restore(backedUp)
			return 0, err
		}
		applied += len(edits)
	}

	r.Applied = true
	r.Backup = backupDir
	return applied, nil
}

// splice applies a file's edits (sorted by offset) to src, verifying each
// replacement before splicing.
func splice(src []byte, edits []Edit) ([]byte, error) {
	sorted := append([]Edit{}, edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Offset > sorted[j].Offset })
	for _, e := range sorted {
		if e.Offset < 0 || e.Offset+len(e.Old) > len(src) {
			return nil, fmt.Errorf("edit out of range in %s", e.File)
		}
		if string(src[e.Offset:e.Offset+len(e.Old)]) != e.Old {
			return nil, fmt.Errorf("source changed since analysis in %s (expected %q)", e.File, e.Old)
		}
		src = append(src[:e.Offset], append([]byte(e.New), src[e.Offset+len(e.Old):]...)...)
	}
	return src, nil
}

func restore(backedUp map[string]string) {
	for abs, bp := range backedUp {
		if b, err := os.ReadFile(bp); err == nil {
			_ = os.WriteFile(abs, b, 0o644)
		}
	}
}

// Render formats a report for terminal/MCP output.
func Render(r *Report) string {
	var b strings.Builder
	if r.Applied {
		b.WriteString(fmt.Sprintf("renamed %s -> %s: %d edits across %d files\n", r.Symbol, r.NewName, len(r.Edits), len(r.Files)))
	} else {
		b.WriteString(fmt.Sprintf("PREVIEW: rename %s -> %s (%d edits across %d files)\n", r.Symbol, r.NewName, len(r.Edits), len(r.Files)))
	}
	if len(r.Defs) > 0 {
		b.WriteString("definitions:\n")
		for _, d := range r.Defs {
			fmt.Fprintf(&b, "  %s:%d\n", d.File, d.Line)
		}
	}
	last := ""
	for _, e := range r.Edits {
		if e.File != last {
			if last != "" {
				b.WriteString("\n")
			}
			last = e.File
		}
		fmt.Fprintf(&b, "  %s:%d:%d %s %s -> %s\n", e.File, e.Line, e.Col, e.Kind, e.Old, e.New)
	}
	for _, s := range r.Skipped {
		b.WriteString("skipped: " + s + "\n")
	}
	if !r.Applied && len(r.Edits) > 0 {
		b.WriteString("run with --apply to commit (files are backed up under .kern/rename-backup/ first)\n")
	}
	if r.Backup != "" {
		b.WriteString("backup: " + r.Backup + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// --- index helpers ----------------------------------------------------------

// packageNameOf returns the declared package name for an import path, resolved
// against the index's package table (keyed by relative Dir(rel)); module-qualified
// import paths are matched by trailing suffix.
func packageNameOf(ix *index.Index, importPath string) string {
	if k := packageDirOf(ix, importPath); k != "" {
		if p := ix.Pkgs[k]; p != nil {
			return p.Name
		}
	}
	return ""
}

// packageDirOf returns the in-project package directory that suffix-matches an
// import path, preferring the LONGEST match. Map iteration is unordered, so
// picking "the first match" would be nondeterministic, and a short dir like
// "util" must never shadow "pkg/util" when both could suffix-match the same
// import path. (A true cross-module name collision with identical directory
// trees cannot be disambiguated without the module path, which the index does
// not record.)
func packageDirOf(ix *index.Index, importPath string) string {
	if ix == nil || importPath == "" {
		return ""
	}
	best := ""
	for k := range ix.Pkgs {
		if importPath == k || strings.HasSuffix(importPath, "/"+k) {
			if len(k) > len(best) {
				best = k
			}
		}
	}
	return best
}

// callerFile resolves a caller symbol name to its defining file, or "".
func callerFile(ix *index.Index, caller string) string {
	for _, m := range ix.Search(caller, 5) {
		if m.FullName() == caller || m.Name == caller {
			return m.File
		}
	}
	return ""
}

// symbolLang returns the language of the file defining a symbol, or "".
func symbolLang(ix *index.Index, file string) string {
	for _, s := range ix.Symbols {
		if s.File == file {
			return s.Lang
		}
	}
	return ""
}

func isExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}
