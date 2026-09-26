package architecture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if _, err := os.Stat(filepath.Join(root, ledgerDocRel)); err != nil {
		t.Fatalf("ledger-details.md not found at %s (cwd %s)", filepath.Join(root, ledgerDocRel), wd)
	}
	return root
}

// The parse/measure/collect machinery behind this test lives in parity.go as
// the exported API (ParseArchDoc, ParseLedgerDetails, MeasureDir, Allowed,
// CheckArchDocParity) so `kern doctor --arch-drift` reports exactly the drift
// this test gates. CheckArchDocParity joins the two ledger halves — the cap
// table in ARCHITECTURE.md and the allowed-deps table in
// docs/architecture/ledger-details.md — and fails if they diverge. This file
// only renders the report as test failures — the logic is shared, never
// duplicated.

func TestArchitectureDocParity(t *testing.T) {
	root := repoRoot(t)
	report, err := CheckArchDocParity(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows < 10 {
		t.Fatalf("ARCHITECTURE.md table parsed only %d rows — table format broken?", report.Rows)
	}
	for _, f := range report.Findings {
		if f.Err != "" {
			t.Errorf("%s: %s", f.Subsystem, f.Err)
			continue
		}
		if f.LOC > f.Cap {
			t.Errorf("%s: LOC %d exceeds cap %d — split the package or raise the cap in ARCHITECTURE.md (suggested cap %d)",
				f.Subsystem, f.LOC, f.Cap, int(float64(f.LOC)*1.5/100)*100+100)
		}
		if len(f.Violations) > 0 {
			t.Errorf("%s: new internal imports outside documented allowed deps: %v — update docs/architecture/ledger-details.md before adding them",
				f.Subsystem, f.Violations)
		}
	}
	// Sanity: the ast parser captures every import form (grouped, single,
	// aliased, dot) and every nested path, so it must validate strictly more
	// internal-import declarations than the old bare-quoted-line scan (~855
	// grouped-form sites, 30 aliased/single-form missed). The declaration
	// count is the direct analog of that 855 baseline.
	if report.Decls <= 855 {
		t.Errorf("strict parser captured only %d internal-import declarations — expected > 855 (old line scan caught 855 grouped-form sites)", report.Decls)
	}
	t.Logf("TestArchitectureDocParity validated %d distinct kern-internal import paths across %d rows from %d internal-import declarations",
		report.ImportPaths, report.Rows, report.Decls)
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
	rows, err := ParseArchDoc(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Dir == "internal/mcp" && row.Cap < 20000 {
			t.Errorf("internal/mcp cap %d too tight for the documented monolith", row.Cap)
		}
		if row.Dir == "internal/blueprint" && row.Cap < 9800 {
			t.Errorf("internal/blueprint cap %d below the settled Stage D cap (9800)", row.Cap)
		}
	}
}

// ---- Two-file ledger fixtures ----------------------------------------------
//
// The ledger is split across ARCHITECTURE.md (dir + LOC baseline + cap) and
// docs/architecture/ledger-details.md (dir + allowed deps). These fixtures
// prove the gate still bites with the split in place: an over-cap directory
// fails via the part-1 table, an import outside allowed deps fails via the
// part-2 table, and divergent halves fail closed (row-count and same-count
// directions).

// writeLedgerFixture builds a temp repo with both halves of the ledger and 12
// subsystems: internal/alpha (over cap, importing a package outside its
// allowed deps), internal/beta (healthy), and ten healthy fillers — enough
// rows to keep the <10-row fail-closed sanity from masking divergence tests.
func writeLedgerFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	var arch, details strings.Builder
	arch.WriteString("# ARCHITECTURE.md — subsystem ledger (part 1)\n\n")
	arch.WriteString("| subsystem | dir | LOC baseline | cap |\n")
	arch.WriteString("|---|---|---|---|\n")
	details.WriteString("# Architecture ledger — part 2: allowed deps\n\n")
	details.WriteString("| subsystem | dir | allowed deps |\n")
	details.WriteString("|---|---|---|\n")
	add := func(dir, baseline, cap, deps string) {
		arch.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", dir, baseline, cap))
		details.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n", dir, dir, deps))
	}
	add("internal/alpha", "5", "10", "`internal/beta`")
	add("internal/beta", "2", "10", "")
	for i := 0; i < 10; i++ {
		add(fmt.Sprintf("internal/sub%02d", i), "1", "100", "")
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(ledgerDocRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, archDocRel), []byte(arch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ledgerDocRel), []byte(details.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// internal/alpha: 30 non-test LOC (cap 10) importing internal/gamma
	// (allowed deps only list internal/beta).
	alpha := "package alpha\n\nimport \"github.com/JayveerPrajapati/kern/internal/gamma\"\n"
	alpha += strings.Repeat("// filler\n", 27)
	if err := os.MkdirAll(filepath.Join(root, "internal", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "alpha", "a.go"), []byte(alpha), 0o644); err != nil {
		t.Fatal(err)
	}
	// internal/beta: 3 non-test LOC, no internal imports.
	beta := "package beta\n\nvar X = 1\n"
	if err := os.MkdirAll(filepath.Join(root, "internal", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "beta", "b.go"), []byte(beta), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ten healthy fillers (3 LOC each, cap 100, no imports).
	for i := 0; i < 10; i++ {
		dir := filepath.Join(root, "internal", fmt.Sprintf("sub%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "s.go"), []byte("package sub\n\nvar X = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestCheckArchDocParityTwoFileLedger proves both enforcement halves bite on
// a fixture: (a) a directory over its cap fails via the ARCHITECTURE.md half,
// (b) an import outside allowed deps fails via the ledger-details.md half.
func TestCheckArchDocParityTwoFileLedger(t *testing.T) {
	root := writeLedgerFixture(t)
	report, err := CheckArchDocParity(root)
	if err != nil {
		t.Fatalf("two-file fixture must parse and join cleanly: %v", err)
	}
	if report.Rows != 12 {
		t.Fatalf("rows = %d, want 12", report.Rows)
	}
	byDir := map[string]*ArchDrift{}
	for i := range report.Findings {
		byDir[report.Findings[i].Subsystem] = &report.Findings[i]
	}
	alpha := byDir["internal/alpha"]
	if alpha == nil {
		t.Fatal("no finding for internal/alpha")
	}
	if alpha.LOC <= alpha.Cap {
		t.Errorf("alpha LOC %d must exceed cap %d — over-cap drift must fail via the ARCHITECTURE.md half", alpha.LOC, alpha.Cap)
	}
	if len(alpha.Violations) != 1 || alpha.Violations[0] != "internal/gamma" {
		t.Errorf("alpha violations = %v, want [internal/gamma] — import outside allowed deps must fail via the ledger-details.md half", alpha.Violations)
	}
	beta := byDir["internal/beta"]
	if beta == nil {
		t.Fatal("no finding for internal/beta")
	}
	if beta.LOC > beta.Cap || len(beta.Violations) != 0 {
		t.Errorf("beta should be healthy, got LOC %d/%d violations %v", beta.LOC, beta.Cap, beta.Violations)
	}
}

// TestCheckArchDocParityTwoFileDivergence proves the row-count-agreement
// sanity: dropping a subsystem from one half fails the whole gate closed.
func TestCheckArchDocParityTwoFileDivergence(t *testing.T) {
	root := writeLedgerFixture(t)
	// Drop internal/beta from the ledger-details half (row-count divergence).
	p := filepath.Join(root, ledgerDocRel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "| `internal/beta` |") {
			continue
		}
		kept = append(kept, ln)
	}
	if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckArchDocParity(root); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("row-count-divergent ledger halves must fail closed with a divergence error, got %v", err)
	}
}

// TestCheckArchDocParityTwoFileSameCountDivergence proves the per-row
// matching sanity: even with equal row counts, a subsystem renamed in one
// half has no matching row in the other and the gate fails closed.
func TestCheckArchDocParityTwoFileSameCountDivergence(t *testing.T) {
	root := writeLedgerFixture(t)
	// Rename internal/beta to internal/gamma in the ledger-details half only.
	p := filepath.Join(root, ledgerDocRel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.ReplaceAll(string(b), "`internal/beta`", "`internal/gamma`")
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckArchDocParity(root); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("same-count divergent halves must fail closed with a divergence error, got %v", err)
	}
}
