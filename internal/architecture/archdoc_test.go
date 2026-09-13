package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// archdoc_test.go implements the ARCHITECTURE.md parity gate (G-P0-4):
// the subsystem table at the repo root is the machine-readable source of
// truth, and this test fails when the code diverges from it — a directory
// missing, LOC past its cap, or a new kern-internal import outside the
// documented allowed set. Updating the architecture means updating
// ARCHITECTURE.md deliberately, never silently.

const (
	archDocRel  = "ARCHITECTURE.md"
	moduleRoot  = "github.com/JayveerPrajapati/kern/"
	internalPfx = moduleRoot + "internal/"
)

var skipDirs = map[string]bool{
	".kern": true, ".git": true, "node_modules": true, "vendor": true,
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, "graphify-out": true, "bin": true, "testfixture": true,
}

type archRow struct {
	dir    string
	cap    int
	imports map[string]bool // allowed kern-internal top-2 segments
}

var archRowRe = regexp.MustCompile(`^\|\s*` + "`" + `(internal/[A-Za-z0-9_]+)` + "`" + `\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|(.*)\|\s*$`)

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

// measureDir returns non-test Go LOC and the set of kern-internal top-2
// segment imports for a directory, mirroring the table's generation rules
// (walk skips generated/vendored dirs and _test.go files).
func measureDir(dir string) (int, map[string]bool, error) {
	loc := 0
	imports := map[string]bool{}
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
		src := string(b)
		loc += strings.Count(src, "\n")
		for _, line := range strings.Split(src, "\n") {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, `"`) || !strings.HasSuffix(t, `"`) {
				continue
			}
			imp := strings.Trim(t, `"`)
			if !strings.HasPrefix(imp, internalPfx) {
				continue
			}
			// Collapse to the top-level subsystem (internal/<first>): the
			// table's allowed deps are top-level dirs and a subsystem's own
			// subpackages are always allowed (internal/blueprint/x imports
			// collapse to internal/blueprint == self).
			segs := strings.Split(strings.TrimPrefix(imp, internalPfx), "/")
			imports["internal/"+segs[0]] = true
		}
		return nil
	})
	return loc, imports, err
}

func TestArchitectureDocParity(t *testing.T) {
	root := repoRoot(t)
	rows := parseArchTable(t, root)
	for _, row := range rows {
		dir := filepath.Join(root, row.dir)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: directory missing", row.dir)
			continue
		}
		loc, imports, err := measureDir(dir)
		if err != nil {
			t.Errorf("%s: measure failed: %v", row.dir, err)
			continue
		}
		if loc > row.cap {
			t.Errorf("%s: LOC %d exceeds cap %d — split the package or raise the cap in ARCHITECTURE.md (suggested cap %d)",
				row.dir, loc, row.cap, int(float64(loc)*1.5/100)*100+100)
		}
		self := "internal/" + filepath.Base(dir)
		var violations []string
		for imp := range imports {
			if imp == self || row.imports[imp] {
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
}

// TestArchitectureDocMentionsKnownDrift pins that the doc records the two
// largest subsystems honestly (the plan's drift examples: mcp monolith,
// blueprint size) instead of hiding them.
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
		if row.dir == "internal/blueprint" && row.cap < 28000 {
			t.Errorf("internal/blueprint cap %d too tight for the documented size", row.cap)
		}
	}
}