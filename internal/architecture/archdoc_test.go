package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	archDocRel = "ARCHITECTURE.md"
	moduleRoot = "github.com/JayveerPrajapati/kern/"
)

var skipDirs = map[string]bool{
	".kern": true, ".git": true, "node_modules": true, "vendor": true,
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, "graphify-out": true, "bin": true, "testfixture": true,
	"sdk": true, "python": true, "dist": true, "docs": true, "homebrew": true,
}

type archRow struct {
	dir     string
	cap     int
	imports map[string]bool // allowed kern-internal paths (each a subtree root)
}

// archRowRe matches table rows whose dir cell is an internal path, allowing
// nested dirs (internal/mcp/doc, internal/blueprint/checks/diffgate) — the
// `/` and `.` are legal in package paths and the nested rows must be parsed
// or their cap/dep enforcement silently vanishes.
var archRowRe = regexp.MustCompile(`^\|\s*` + "`" + `(internal/[A-Za-z0-9_/.]+)` + "`" + `\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|(.*)\|\s*$`)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Tests run with cwd = the package directory (internal/architecture).
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, archDocRel)); err != nil {
		t.Fatalf("ARCHITECTURE.md not found at %s (cwd %s)", filepath.Join(root, archDocRel), wd)
	}
	return root
}

func parseArchTable(t *testing.T, root string) []archRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, archDocRel))
	if err != nil {
		t.Fatal(err)
	}
	var rows []archRow
	for _, line := range strings.Split(string(b), "\n") {
		m := archRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		row := archRow{dir: m[1], imports: map[string]bool{}}
		cap := 0
		for _, ch := range m[3] {
			if ch < '0' || ch > '9' {
				break
			}
			cap = cap*10 + int(ch-'0')
		}
		row.cap = cap
		for _, dep := range strings.Fields(m[4]) {
			dep = strings.Trim(dep, "`")
			if strings.HasPrefix(dep, "internal/") {
				row.imports[dep] = true
			}
		}
		rows = append(rows, row)
	}
	if len(rows) < 10 {
		t.Fatalf("ARCHITECTURE.md table parsed only %d rows — table format broken?", len(rows))
	}
	return rows
}

// measureDir returns non-test Go LOC and the set of kern-internal import
// paths for a directory, mirroring the table's generation rules (walk skips
// generated/vendored dirs and _test.go files). Imports are parsed with
// go/parser + go/ast so every form is captured — grouped, single-form,
// aliased, and dot-imports (dot-imports count by their resolved path, they
// are not special-cased) — and keys are the FULL resolved package paths
// (e.g. internal/mcp/gov), not collapsed top-level prefixes: the nested mcp
// rows are therefore enforced, and an allowed-dep entry acts as a subtree
// root (internal/foo permits internal/foo/**).
func measureDir(dir string) (int, map[string]bool, int, error) {
	loc := 0
	imports := map[string]bool{}
	decls := 0 // internal-import declarations seen (all forms), for the strictness sanity
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		loc += strings.Count(string(b), "\n")
		f, err := parser.ParseFile(token.NewFileSet(), p, b, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		for _, imp := range f.Imports {
			// Resolve the import relative to the module root: keep only the
			// internal/<subsystem>[/<leaf>...] part as the map key.
			path := strings.TrimPrefix(strings.Trim(imp.Path.Value, `"`), moduleRoot)
			if !strings.HasPrefix(path, "internal/") {
				continue
			}
			imports[path] = true
			decls++
		}
		return nil
	})
	return loc, imports, decls, err
}

// allowed reports whether imp lies inside any allowed dep's subtree. An
// allowed entry is a subtree root: internal/domain covers internal/domain
// itself and internal/domain/x; internal/mcp/gov covers that exact leaf.
func allowed(imp string, deps map[string]bool) bool {
	for dep := range deps {
		if imp == dep || strings.HasPrefix(imp, dep+"/") {
			return true
		}
	}
	return false
}

func TestArchitectureDocParity(t *testing.T) {
	root := repoRoot(t)
	rows := parseArchTable(t, root)
	validated := 0
	totalDecls := 0
	for _, row := range rows {
		dir := filepath.Join(root, row.dir)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: directory missing", row.dir)
			continue
		}
		loc, imports, decls, err := measureDir(dir)
		if err != nil {
			t.Errorf("%s: measure failed: %v", row.dir, err)
			continue
		}
		if loc > row.cap {
			t.Errorf("%s: LOC %d exceeds cap %d — split the package or raise the cap in ARCHITECTURE.md (suggested cap %d)",
				row.dir, loc, row.cap, int(float64(loc)*1.5/100)*100+100)
		}
		validated += len(imports)
		totalDecls += decls
		self := row.dir // full internal path, nested dirs included
		var violations []string
		for imp := range imports {
			// A subsystem's own subpackages are always allowed.
			if imp == self || strings.HasPrefix(imp, self+"/") {
				continue
			}
			if allowed(imp, row.imports) {
				continue
			}
			violations = append(violations, imp)
		}
		if len(violations) > 0 {
			sort.Strings(violations)
			t.Errorf("%s: new internal imports outside documented allowed deps: %v — update ARCHITECTURE.md before adding them",
				row.dir, violations)
		}
	}
	// Sanity: the ast parser captures every import form (grouped, single,
	// aliased, dot) and every nested path, so it must validate strictly more
	// internal-import declarations than the old bare-quoted-line scan (~855
	// grouped-form sites, 30 aliased/single-form missed). The declaration
	// count is the direct analog of that 855 baseline.
	if totalDecls <= 855 {
		t.Errorf("strict parser captured only %d internal-import declarations — expected > 855 (old line scan caught 855 grouped-form sites)", totalDecls)
	}
	t.Logf("TestArchitectureDocParity validated %d distinct kern-internal import paths across %d rows from %d internal-import declarations",
		validated, len(rows), totalDecls)
}

// TestArchitectureDocMentionsKnownDrift pins that the doc records the
// largest subsystem honestly (the plan's drift example: mcp monolith)
// instead of hiding it, and keeps the settled blueprint cap (9800, Stage D
// follow-up) from regressing.
func TestArchitectureDocMentionsKnownDrift(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, archDocRel))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.Contains(doc, "internal/mcp") || !strings.Contains(doc, "internal/blueprint") {
		t.Error("ARCHITECTURE.md must document the known drift (mcp monolith, blueprint size) explicitly")
	}
	rows := parseArchTable(t, root)
	for _, row := range rows {
		if row.dir == "internal/mcp" && row.cap < 20000 {
			t.Errorf("internal/mcp cap %d too tight for the documented monolith", row.cap)
		}
		if row.dir == "internal/blueprint" && row.cap < 9800 {
			t.Errorf("internal/blueprint cap %d below the settled Stage D cap (9800)", row.cap)
		}
	}
}
