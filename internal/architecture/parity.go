package architecture

// This file exports the two-file architecture-ledger parity checker that
// TestArchitectureDocParity enforces as a programmatic API, so `kern doctor
// --arch-drift` can report the same drift (per-subsystem LOC caps from
// ARCHITECTURE.md + allowed-deps from docs/architecture/ledger-details.md
// against actual imports) without duplicating the parser or the collector.
// The test remains the authoritative gate; the exported surface is the single
// shared implementation.

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// archDocRel is the repo-root-relative path of part 1 of the machine-read
// ledger: the subsystem table carrying dir, informational LOC baseline, and
// the cap (the LOC drift gate).
const archDocRel = "ARCHITECTURE.md"

// ledgerDocRel is the repo-root-relative path of part 2 of the machine-read
// ledger: the per-subsystem allowed-deps column and the full changelog,
// parsed by the same parity test and joined to part 1 by dir.
const ledgerDocRel = "docs/architecture/ledger-details.md"

// moduleRoot is the Go module import prefix; internal imports are resolved
// relative to it so only the internal/<subsystem>[/<leaf>...] part is kept.
const moduleRoot = "github.com/JayveerPrajapati/kern/"

// skipDirs are directories the LOC/import measurement never walks into
// (generated/vendored/foreign trees), mirroring the table's generation rules.
var skipDirs = map[string]bool{
	".kern": true, ".git": true, "node_modules": true, "vendor": true,
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, "graphify-out": true, "bin": true, "testfixture": true,
	"sdk": true, "python": true, "dist": true, "docs": true, "homebrew": true,
}

// ArchRow is one subsystem row of the machine-read ledger. Part 1
// (ARCHITECTURE.md) fills Dir + Cap; part 2
// (docs/architecture/ledger-details.md) fills Dir + Imports; CheckArchDocParity
// joins the two halves by Dir and fails if they diverge.
type ArchRow struct {
	Dir     string
	Cap     int
	Imports map[string]bool // allowed kern-internal paths (each a subtree root)
}

// archRowRe matches table rows whose dir cell is an internal or cmd path,
// allowing nested dirs (internal/mcp/doc, internal/blueprint/checks/diffgate,
// cmd/kern) — the `/` and `.` are legal in package paths and the nested rows
// must be parsed or their cap enforcement silently vanishes. Rows carry four
// cells (dir | LOC baseline | cap); a trailing allowed-deps cell from the
// pre-split single-file format is tolerated and ignored — deps now live in
// ledger-details.md.
var archRowRe = regexp.MustCompile(`^\|\s*` + "`" + `((?:internal|cmd)/[A-Za-z0-9_/.]+)` + "`" + `\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|(?:.*\|)?\s*$`)

// ParseArchDoc parses the cap half of the ledger out of <root>/ARCHITECTURE.md:
// one row per subsystem (dir + cap; the LOC baseline cell is informational and
// skipped). The allowed-deps half lives in <root>/docs/architecture/
// ledger-details.md (ParseLedgerDetails); CheckArchDocParity joins the two
// halves by dir and fails on divergence. Both parsers fail closed on an
// unreadable doc or a table with fewer than 10 rows (the same sanity the
// drift test pins), never returning a silently-broken parse.
func ParseArchDoc(root string) ([]ArchRow, error) {
	b, err := os.ReadFile(filepath.Join(root, archDocRel))
	if err != nil {
		return nil, err
	}
	var rows []ArchRow
	for _, line := range strings.Split(string(b), "\n") {
		m := archRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		row := ArchRow{Dir: m[1], Imports: map[string]bool{}}
		cap := 0
		for _, ch := range m[3] {
			if ch < '0' || ch > '9' {
				break
			}
			cap = cap*10 + int(ch-'0')
		}
		row.Cap = cap
		rows = append(rows, row)
	}
	if len(rows) < 10 {
		return nil, fmt.Errorf("%s: table parsed only %d rows — table format broken?", archDocRel, len(rows))
	}
	return rows, nil
}

// ledgerRowRe matches table rows in the ledger-details table, whose cells are
// subsystem | dir | allowed deps — the subsystem identity column IS the dir
// (the same identity part 1 uses), and the deps cell is the verbatim
// allowed-deps set, each entry a subtree root.
var ledgerRowRe = regexp.MustCompile(`^\|\s*` + "`" + `((?:internal|cmd)/[A-Za-z0-9_/.]+)` + "`" + `\s*\|\s*` + "`" + `((?:internal|cmd)/[A-Za-z0-9_/.]+)` + "`" + `\s*\|(.*)\|\s*$`)

// ParseLedgerDetails parses the allowed-deps half of the ledger out of
// <root>/docs/architecture/ledger-details.md: one row per subsystem with the
// deps cell copied verbatim from the pre-split single-file ledger. Fails
// closed on an unreadable doc, a row whose subsystem and dir cells disagree,
// or a table with fewer than 10 rows.
func ParseLedgerDetails(root string) ([]ArchRow, error) {
	b, err := os.ReadFile(filepath.Join(root, ledgerDocRel))
	if err != nil {
		return nil, err
	}
	var rows []ArchRow
	for _, line := range strings.Split(string(b), "\n") {
		m := ledgerRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] != m[2] {
			return nil, fmt.Errorf("%s: subsystem %q != dir %q — ledger identity cells diverged", ledgerDocRel, m[1], m[2])
		}
		row := ArchRow{Dir: m[1], Imports: map[string]bool{}}
		for _, dep := range strings.Fields(m[3]) {
			dep = strings.Trim(dep, "`")
			if strings.HasPrefix(dep, "internal/") {
				row.Imports[dep] = true
			}
		}
		rows = append(rows, row)
	}
	if len(rows) < 10 {
		return nil, fmt.Errorf("%s: table parsed only %d rows — table format broken?", ledgerDocRel, len(rows))
	}
	return rows, nil
}

// MeasureDir returns non-test Go LOC and the set of kern-internal import
// paths for a directory, mirroring the table's generation rules (walk skips
// generated/vendored dirs and _test.go files). Imports are parsed with
// go/parser + go/ast so every form is captured — grouped, single-form,
// aliased, and dot-imports (dot-imports count by their resolved path, they
// are not special-cased) — and keys are the FULL resolved package paths
// (e.g. internal/mcp/gov), not collapsed top-level prefixes: the nested mcp
// rows are therefore enforced, and an allowed-dep entry acts as a subtree
// root (internal/foo permits internal/foo/**).
func MeasureDir(dir string) (loc int, imports map[string]bool, decls int, err error) {
	imports = map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
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

// Allowed reports whether imp lies inside any allowed dep's subtree. An
// allowed entry is a subtree root: internal/domain covers internal/domain
// itself and internal/domain/x; internal/mcp/gov covers that exact leaf.
func Allowed(imp string, deps map[string]bool) bool {
	for dep := range deps {
		if imp == dep || strings.HasPrefix(imp, dep+"/") {
			return true
		}
	}
	return false
}

// ArchDrift is the drift state of one subsystem row in ARCHITECTURE.md.
type ArchDrift struct {
	Subsystem  string   // dir from the table row (internal/...)
	LOC        int      // measured non-test Go LOC
	Cap        int      // documented LOC cap
	Violations []string // sorted new internal imports outside allowed deps (empty = none)
	Err        string   // structural problem (missing dir, measure failure); empty when OK
}

// ArchDocReport is the full parity outcome for one ARCHITECTURE.md table.
type ArchDocReport struct {
	Rows        int
	ImportPaths int // distinct kern-internal import paths validated
	Decls       int // internal-import declarations seen (all forms)
	Findings    []ArchDrift
}

// CheckArchDocParity runs the same validation TestArchitectureDocParity
// enforces, across BOTH halves of the machine-read ledger: for every
// subsystem row, measure non-test Go LOC against the cap (parsed from
// ARCHITECTURE.md) and collect internal imports that fall outside the
// documented allowed deps (parsed from docs/architecture/ledger-details.md;
// a subsystem's own subpackages are always allowed). The two halves must
// name exactly the same subsystems — divergence fails closed. Every row is
// reported (LOC current vs cap), with Violations non-empty only where drift
// exists. Hard failures — an unreadable ARCHITECTURE.md or ledger-details.md,
// a table that no longer parses, or half-divergence — are returned as an
// error; per-row structural problems (missing directory, unmeasurable
// source) are carried on the row's Err field so the caller can surface them
// without losing the rest of the report.
func CheckArchDocParity(root string) (*ArchDocReport, error) {
	rows, err := ParseArchDoc(root)
	if err != nil {
		return nil, err
	}
	details, err := ParseLedgerDetails(root)
	if err != nil {
		return nil, err
	}
	// Sanity: the two ledger halves must name exactly the same subsystems —
	// every row in ARCHITECTURE.md has exactly one matching row in
	// ledger-details.md and vice versa. Divergence means a subsystem was
	// silently dropped from one half; fail closed rather than enforce a
	// partial ledger.
	if len(details) != len(rows) {
		return nil, fmt.Errorf("%s: %d rows vs %s: %d rows — the two ledger halves diverged", archDocRel, len(rows), ledgerDocRel, len(details))
	}
	depsByDir := make(map[string]map[string]bool, len(details))
	for _, d := range details {
		depsByDir[d.Dir] = d.Imports
	}
	report := &ArchDocReport{Rows: len(rows)}
	for _, row := range rows {
		deps, ok := depsByDir[row.Dir]
		if !ok {
			return nil, fmt.Errorf("%s: subsystem %s has no matching row in %s — the two ledger halves diverged", archDocRel, row.Dir, ledgerDocRel)
		}
		drift := ArchDrift{Subsystem: row.Dir, Cap: row.Cap}
		dir := filepath.Join(root, row.Dir)
		if _, err := os.Stat(dir); err != nil {
			drift.Err = "directory missing"
			report.Findings = append(report.Findings, drift)
			continue
		}
		loc, imports, decls, err := MeasureDir(dir)
		if err != nil {
			drift.Err = "measure failed: " + err.Error()
			report.Findings = append(report.Findings, drift)
			continue
		}
		drift.LOC = loc
		report.Decls += decls
		report.ImportPaths += len(imports)
		self := row.Dir // full internal path, nested dirs included
		for imp := range imports {
			// A subsystem's own subpackages are always allowed.
			if imp == self || strings.HasPrefix(imp, self+"/") {
				continue
			}
			if Allowed(imp, deps) {
				continue
			}
			drift.Violations = append(drift.Violations, imp)
		}
		if len(drift.Violations) > 0 {
			sort.Strings(drift.Violations)
		}
		report.Findings = append(report.Findings, drift)
	}
	return report, nil
}
