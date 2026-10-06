package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// F12 (campaign 2026-10-04): E2E coverage of the REAL pipeline on non-Go
// fixtures — the gap that would have caught findings 1, 2, 7 and 8 before
// the live black-box campaign did. Every fixture lives in t.TempDir() and
// every assertion goes through the real library calls (index.Build, intel
// resolver/explore/graph queries) — no hand-built indexes.

// writeNonGoTree writes rel->content under dir, creating parent directories.
func writeNonGoTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

const pyModuleSrc = `class AuditLogger:
    def __init__(self, name):
        self.name = name

    def login(self, token):
        return jwt_decode(token)

    def record(self, event):
        return format_event(event)

def format_event(event):
    return "event:" + event

def run_checks(path):
    logger = AuditLogger(path)
    return logger.login("tok")

default_logger = AuditLogger("default")
`

const pyTestSrc = `from log_checker import AuditLogger, run_checks

def test_login():
    logger = AuditLogger("x")
    assert logger.login("tok")

def test_run_checks():
    assert run_checks("p")
`

// writePythonRepo writes a Python repo: a module (class + module functions),
// a pytest file calling them, and the packaging artifacts the index must skip
// (egg-info dir + a package-lock.json carrying a distinctive prop key).
// jwt_decode is deliberately NOT defined in the repo: blast-radius leaves
// that resolve to no indexed symbol are the exact F1 shape.
func writePythonRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeNonGoTree(t, dir, map[string]string{
		"log_checker.py":      pyModuleSrc,
		"test_log_checker.py": pyTestSrc,
		"log_checker.egg-info/PKG-INFO": `Metadata-Version: 2.1
Name: log-checker
Version: 1.0
`,
		"log_checker.egg-info/SOURCES.txt": `log_checker.py
test_log_checker.py
`,
		"package-lock.json": `{
  "name": "log-checker",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "zzlockdistpropkey": {"nested": 1},
  "packages": {"": {"name": "log-checker"}}
}
`,
	})
	return dir
}

const tsModuleSrc = `export interface CartItem {
  sku: string
  price: number
}

export function computeTotal(items: CartItem[]): number {
  let total = 0
  for (const item of items) {
    total += applyDiscount(item.price)
  }
  return total
}

function applyDiscount(price: number): number {
  return price * 0.9
}

export const DEFAULT_TOTAL = computeTotal([])
`

const tsTestSrc = `import { computeTotal } from "./cart"

test("computes total", () => {
  expect(computeTotal([{ sku: "a", price: 10 }])).toBe(9)
})
`

func writeJSRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeNonGoTree(t, dir, map[string]string{
		"cart.ts":      tsModuleSrc,
		"cart.test.ts": tsTestSrc,
	})
	return dir
}

func buildNonGoIndex(t *testing.T, dir string) *index.Index {
	t.Helper()
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("index.Build(%s): %v", dir, err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatalf("index built with 0 symbols")
	}
	return ix
}

func findFullSym(t *testing.T, ix *index.Index, full string) *index.Symbol {
	t.Helper()
	for i := range ix.Symbols {
		if ix.Symbols[i].FullName() == full {
			return &ix.Symbols[i]
		}
	}
	return nil
}

// TestNonGoIndexPythonSymbols pins the index side of F8 at E2E level: the
// real Python symbols are all present, and the egg-info / package-lock
// artifacts contribute no symbols (the distinctive lockfile key must not
// appear as a prop).
func TestNonGoIndexPythonSymbols(t *testing.T) {
	ix := buildNonGoIndex(t, writePythonRepo(t))

	if s := findFullSym(t, ix, "AuditLogger"); s == nil || s.Kind != "class" || s.Lang != "python" {
		t.Errorf("expected python class AuditLogger, got %+v", s)
	}
	if s := findFullSym(t, ix, "AuditLogger.login"); s == nil || s.Kind != "method" {
		t.Errorf("expected method AuditLogger.login, got %+v", s)
	}
	if s := findFullSym(t, ix, "format_event"); s == nil || s.Kind != "func" {
		t.Errorf("expected func format_event, got %+v", s)
	}
	if s := findFullSym(t, ix, "run_checks"); s == nil || s.Kind != "func" {
		t.Errorf("expected func run_checks, got %+v", s)
	}

	for _, s := range ix.Symbols {
		if strings.Contains(s.File, ".egg-info") {
			t.Errorf("egg-info artifact indexed as symbol: %+v", s)
		}
		if s.File == "package-lock.json" {
			t.Errorf("package-lock.json indexed as symbol source: %+v", s)
		}
		if s.Name == "zzlockdistpropkey" || s.Name == "lockfileVersion" || s.Name == "packages" {
			t.Errorf("lockfile prop leaked into the index: %+v", s)
		}
	}
}

// TestExplorePythonClassNoPanic is the E2E F1 pin: exploring a Python class
// (and its method, whose callee jwt_decode resolves to no indexed symbol)
// with depth>0 and maxNodes>0 must return a report, not panic, and every
// BlastFiles entry must be a real fixture file (no misattribution).
func TestExplorePythonClassNoPanic(t *testing.T) {
	ix := buildNonGoIndex(t, writePythonRepo(t))

	for _, target := range []string{"AuditLogger", "AuditLogger.login"} {
		rep, err := intel.ExploreBudgeted(ix, target, 2, 30, "", 0)
		if err != nil {
			t.Fatalf("ExploreBudgeted(%s) error = %v, want nil (pre-fix this panicked with index out of range)", target, err)
		}
		if rep == nil || rep.Resolved == "" {
			t.Fatalf("ExploreBudgeted(%s) returned an empty report", target)
		}
		if len(rep.BlastFiles) == 0 {
			t.Errorf("ExploreBudgeted(%s) BlastFiles is empty; want at least the fixture module", target)
		}
		for _, f := range rep.BlastFiles {
			if f != "log_checker.py" && f != "test_log_checker.py" {
				t.Errorf("ExploreBudgeted(%s) BlastFiles = %v; %q is not a real fixture file (misattribution)", target, rep.BlastFiles, f)
			}
		}
	}
}

// TestExploreJSFunctionNoPanic is F1's other live case: exploring a
// TypeScript function must return a report, not panic.
func TestExploreJSFunctionNoPanic(t *testing.T) {
	ix := buildNonGoIndex(t, writeJSRepo(t))

	rep, err := intel.Explore(ix, "computeTotal", 2, 30)
	if err != nil {
		t.Fatalf("Explore(computeTotal) error = %v, want nil", err)
	}
	if rep == nil || rep.Resolved == "" {
		t.Fatal("Explore(computeTotal) returned an empty report")
	}
	for _, f := range rep.BlastFiles {
		if f != "cart.ts" && f != "cart.test.ts" {
			t.Errorf("Explore(computeTotal) BlastFiles = %v; %q is not a real fixture file", rep.BlastFiles, f)
		}
	}
}

// TestNonGoBareMethodResolves is the E2E F2 pin: a bare method/function name
// must resolve through intel.Resolve (the resolver impact/explore fall back
// to) and be explorable.
func TestNonGoBareMethodResolves(t *testing.T) {
	ix := buildNonGoIndex(t, writePythonRepo(t))

	resolved, ok := intel.Resolve(ix, "login")
	if !ok {
		t.Fatal("intel.Resolve(login) did not resolve the bare method name")
	}
	if resolved != "AuditLogger.login" {
		t.Errorf("intel.Resolve(login) = %q, want AuditLogger.login", resolved)
	}
	if resolved, ok := intel.Resolve(ix, "run_checks"); !ok || resolved != "run_checks" {
		t.Errorf("intel.Resolve(run_checks) = %q, %v; want run_checks, true", resolved, ok)
	}
	if _, err := intel.Explore(ix, "login", 1, 10); err != nil {
		t.Errorf("Explore(bare method login) error = %v, want nil", err)
	}
}

// TestPytestWhatTestsCover is the E2E F7 pin: WhatTestsCoverPrecise — the
// query behind both "Tests that cover it" paths (impact render and what-if
// Simulate) — must count pytest callers of a Python function (> 0), and
// every covering node must be a pytest test.
func TestPytestWhatTestsCover(t *testing.T) {
	ix := buildNonGoIndex(t, writePythonRepo(t))
	g := intel.FromIndex(ix)

	cover := g.WhatTestsCoverPrecise("run_checks", false)
	if len(cover) == 0 {
		t.Fatal("WhatTestsCoverPrecise(run_checks) = 0 covering tests; want > 0 (pytest callers must count)")
	}
	sawRunChecksTest := false
	for _, n := range cover {
		if !strings.Contains(n.ID, "test_") {
			t.Errorf("covering node %q is not a pytest test", n.ID)
		}
		if strings.Contains(n.ID, "test_run_checks") {
			sawRunChecksTest = true
		}
	}
	if !sawRunChecksTest {
		t.Errorf("WhatTestsCoverPrecise(run_checks) = %v; missing the direct pytest caller test_run_checks", nodeIDs(cover))
	}
}

// TestNonGoSearchSkipsLockfileKeys is the E2E F8 search pin: a real Python
// symbol is findable, the lockfile-only key is not.
func TestNonGoSearchSkipsLockfileKeys(t *testing.T) {
	ix := buildNonGoIndex(t, writePythonRepo(t))

	if hits := ix.Search("AuditLogger", 10); len(hits) == 0 {
		t.Error("Search(AuditLogger) found nothing; want the python class")
	} else if hits[0].FullName() != "AuditLogger" {
		t.Errorf("Search(AuditLogger) top hit = %q, want AuditLogger", hits[0].FullName())
	}
	if hits := ix.Search("run_checks", 10); len(hits) == 0 {
		t.Error("Search(run_checks) found nothing; want the python func")
	}
	if hits := ix.Search("zzlockdistpropkey", 10); len(hits) != 0 {
		t.Errorf("Search(zzlockdistpropkey) = %v; lockfile-only key must not be searchable", symFullNames(hits))
	}
}

func nodeIDs(nodes []domain.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func symFullNames(syms []index.Symbol) []string {
	out := make([]string, len(syms))
	for i := range syms {
		out[i] = syms[i].FullName()
	}
	return out
}
