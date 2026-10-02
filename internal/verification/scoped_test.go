package verification

import (
	"strings"
	"testing"
)

// TestWithTestPackagesScopesTestArgs pins the scoping contract at the
// command level: default = ./..., scoped = only the given patterns,
// fullTests + scoped = both honored, and the override path is untouched.
func TestWithTestPackagesScopesTestArgs(t *testing.T) {
	e := NewEngine(t.TempDir())
	if got := strings.Join(e.testArgs(), " "); got != "test -v -short ./..." {
		t.Fatalf("default args = %q, want %q", got, "test -v -short ./...")
	}

	e = NewEngine(t.TempDir()).WithTestPackages([]string{"./internal/a", "./internal/b"})
	if got := strings.Join(e.testArgs(), " "); got != "test -v -short ./internal/a ./internal/b" {
		t.Fatalf("scoped args = %q, want %q", got, "test -v -short ./internal/a ./internal/b")
	}

	e = NewEngine(t.TempDir()).WithTestPackages([]string{"./internal/a"}).WithFullTests(true)
	if got := strings.Join(e.testArgs(), " "); got != "test -v ./internal/a" {
		t.Fatalf("full+scoped args = %q, want %q", got, "test -v ./internal/a")
	}

	// nil / empty normalizes to the whole-module default.
	e = NewEngine(t.TempDir()).WithTestPackages(nil)
	if got := strings.Join(e.testArgs(), " "); got != "test -v -short ./..." {
		t.Fatalf("nil-scoped args = %q, want default ./...", got)
	}
}

// TestVerifyTestsScopedPackageRunsOnlyScope pins the behavioral contract the
// closed loop relies on: a scoped engine runs go test only on the requested
// packages (a deliberately failing out-of-scope package must not fail the
// run) and reports the scope in the result label.
func TestVerifyTestsScopedPackageRunsOnlyScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":            "module scoped.test\n\ngo 1.23\n",
		"good/good.go":      "package good\n\nfunc Good() int { return 1 }\n",
		"good/good_test.go": "package good\n\nimport \"testing\"\n\nfunc TestGood(t *testing.T) { if Good() != 1 { t.Fatal() } }\n",
		"bad/bad.go":        "package bad\n\nfunc Bad() int { return 1 }\n",
		"bad/bad_test.go":   "package bad\n\nimport \"testing\"\n\nfunc TestBad(t *testing.T) { t.Fatal(\"this package must NOT run when scoped away\") }\n",
	})
	res := NewEngine(dir).WithTestPackages([]string{"./good"}).VerifyTests()
	if res == nil {
		t.Fatal("nil test result")
	}
	if !res.OK {
		t.Errorf("scoped run must pass (out-of-scope failure must not leak in): %s", trunc(res.Output))
	}
	if res.Package != "./good" {
		t.Errorf("result label = %q, want %q", res.Package, "./good")
	}
}
