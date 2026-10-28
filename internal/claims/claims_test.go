package claims

import (
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// symbolAt builds a minimal symbol for query-construction tests.
func symbolAt(file, name string) index.Symbol {
	return index.Symbol{File: file, Name: name}
}

// TestGateCorpusClaimPasses exercises the real cross-oracle corpus: the
// bash-guard and governed-exec classifiers must agree on every command.
func TestGateCorpusClaimPasses(t *testing.T) {
	got := claimGateCorpus(t.TempDir())
	if got.Status != StatusPass {
		t.Fatalf("gate corpus claim = %s (%s); evidence: %v", got.Status, got.Detail, got.Evidence)
	}
}

// TestRunResolvesRootBeforeClaims pins the 2026-10-27 fix: `kern verify claims`
// (no --root) used to pass "." into the guard-corpus claim, where the
// path-aware write arm could not resolve relative targets — so every in-tree
// move looked out-of-tree and the claim failed on a binary that satisfies it.
// The corpus now runs against an absolute root either way.
func TestRunResolvesRootBeforeClaims(t *testing.T) {
	rep := Run(".")
	if rep.Root == "." || !filepath.IsAbs(rep.Root) {
		t.Fatalf("Run must resolve the root before claims run, got %q", rep.Root)
	}
	for _, c := range rep.Claims {
		if c.ID == "guard-gate-corpus" && c.Status == StatusFail {
			t.Fatalf("guard-corpus claim failed with a relative root: %s %v", c.Detail, c.Evidence)
		}
	}
}

func TestClaimToolCatalogPasses(t *testing.T) {
	got := claimToolCatalog()
	if got.Status != StatusPass {
		t.Fatalf("tool catalog claim = %s (%s); evidence: %v", got.Status, got.Detail, got.Evidence)
	}
}

func TestReportMath(t *testing.T) {
	rep := Report{Claims: []Result{
		{ID: "a", Status: StatusPass},
		{ID: "b", Status: StatusSkip},
		{ID: "c", Status: StatusFail},
	}}
	for _, c := range rep.Claims {
		switch c.Status {
		case StatusFail:
			rep.OK = false
			rep.Failed++
		case StatusSkip:
			rep.Skipped++
		}
	}
	if rep.OK {
		t.Fatal("a report with a FAIL must not be OK")
	}
	if rep.Failed != 1 || rep.Skipped != 1 {
		t.Fatalf("Failed=%d Skipped=%d, want 1/1", rep.Failed, rep.Skipped)
	}
}

func TestQualifiedQuery(t *testing.T) {
	// A directory-qualified query is what binds the exact declaration when a
	// bare name collides across packages.
	if got := qualifiedQuery(symbolAt("internal/domain/domain.go", "Risk")); got != "internal/domain.Risk" {
		t.Fatalf("qualifiedQuery = %q", got)
	}
	if got := qualifiedQuery(symbolAt("main.go", "Root")); got != "Root" {
		t.Fatalf("root-package query = %q", got)
	}
}
