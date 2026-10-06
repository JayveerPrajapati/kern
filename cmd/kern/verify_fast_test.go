package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fastVerifyFixture builds a tiny Go module in a git repo with a base
// commit; changeA=true leaves an uncommitted modification in a/a.go so
// changedTestPackages reports exactly ./a.
func fastVerifyFixture(t *testing.T, changeA bool) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.23\n")
	write("a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	if changeA {
		write("a/a.go", "package a\n\nfunc A() int { return 42 }\n")
	}
	return dir
}

// TestRunVerifyFastBuildOnlyFallback pins the fast tier's build-only
// fallback: a bare `kern verify --fast` is a valid request (never the
// no-checks usage error), the mode note prints, and with no changed Go
// packages (here: not even a git repo) the test step is dropped entirely —
// build-only, never a silent empty test run.
func TestRunVerifyFastBuildOnlyFallback(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "a.go"), []byte("package a\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runVerify([]string{"--fast", "--root", dir}) })
	for _, want := range []string{
		"fast mode (build + changed tests)",
		"no changed Go packages — running build only",
		"build: OK",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--fast output missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tests:") {
		t.Fatalf("--fast with no changed packages must be build-only (no test step), got:\n%s", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("--fast build-only run must not FAIL, got:\n%s", out)
	}
}

// TestRunVerifyFastScopesChangedPackages pins the fast tier's incremental
// scoping: with uncommitted changes, the test step narrows to exactly the
// changed packages while -short stays the default (never --full).
func TestRunVerifyFastScopesChangedPackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := fastVerifyFixture(t, true)
	out := captureStdout(t, func() { runVerify([]string{"--fast", "--root", dir}) })
	for _, want := range []string{
		"fast mode (build + changed tests)",
		"changed packages (1): ./a",
		"build: OK",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--fast output missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("--fast changed-scoped run must not FAIL, got:\n%s", out)
	}
}

// TestRunVerifyFullBeatsFast pins the interaction rule: when --fast and
// --full are both given, --full wins — the full-suite mode note prints and
// --fast's implied changed-test scoping is suppressed (no "changed packages"
// line) even though the tree has uncommitted changes.
func TestRunVerifyFullBeatsFast(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := fastVerifyFixture(t, true) // uncommitted change in ./a would scope under --fast alone
	out := captureStdout(t, func() { runVerify([]string{"--fast", "--full", "--root", dir}) })
	if !strings.Contains(out, "full suite (short mode: kern verify -short)") {
		t.Fatalf("--fast --full must run the full suite, got:\n%s", out)
	}
	for _, banned := range []string{"fast mode", "changed packages", "no changed Go packages"} {
		if strings.Contains(out, banned) {
			t.Fatalf("--fast --full must ignore --fast entirely (found %q), got:\n%s", banned, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("--fast --full run must not FAIL, got:\n%s", out)
	}
}
