package highlevel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
)

// verifyTiersFixture builds a tiny Go module in a git repo with a base
// commit: packages a and b, where b carries a committed test that FAILS only
// under the complete (non-short) suite (it skips under -short). That one test
// proves the tier semantics behaviorally:
//   - fast scoping that excludes ./b must PASS (the failure never runs);
//   - the build-only fallback must PASS (no test step at all);
//   - full=true must FAIL (the full-only failure runs);
//   - full beats fast (full runs, failure runs, no fast notes).
//
// The base commit uses `git -c core.hooksPath=` because global hooks are
// installed on dev machines. The returned tree is clean vs HEAD; tests add
// uncommitted .go files to scope fast runs.
func verifyTiersFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		// -c core.hooksPath= neutralizes globally installed git hooks on dev
		// machines; the rest of args are appended after the flag.
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath="}, args...)...)
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
	write(".gitignore", "/.kern\n")
	write("go.mod", "module example.com/m\n\ngo 1.23\n")
	write("a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	write("b/b_test.go", `package b

import "testing"

func TestFullOnlyFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode skips this full-suite-only failure")
	}
	t.Fatal("full-suite-only failure")
}
`)
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir
}

// verifyHooks wires the injected PlatformFor hook to a real app.Platform so
// the fast/full options reach the verification engine end to end.
func verifyHooks(root string) Hooks {
	return Hooks{
		PlatformFor: func(ctx context.Context, r string) (*app.Platform, error) {
			return app.New(r)
		},
	}
}

// setVerifyExecEnv opts the test into the governed exec checks (build/test
// shell out) and a hermetic Go build cache, mirroring the root mcp package's
// verify tests.
func setVerifyExecEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_NET", "1") // fail-closed gate: unisolated runs on hosts without netns (darwin)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// TestVerifyFastScopesChangedPackages pins the fast tier: fast=true forces
// build+test and scopes the test step to the packages with uncommitted .go
// changes. An untracked file in ./a scopes to exactly ./a — the committed
// full-only failure in the unchanged ./b must never run (a PASS proves the
// scope excluded it; an unscoped run would FAIL).
func TestVerifyFastScopesChangedPackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setVerifyExecEnv(t)
	dir := verifyTiersFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "a", "extra.go"), []byte("package a\n\nfunc Extra() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Verify(context.Background(), verifyHooks(dir), map[string]any{"root": dir, "types": "build,test", "fast": true})
	if err != nil {
		t.Fatalf("Verify(fast=true): %v", err)
	}
	for _, want := range []string{"changed packages (1): ./a", "verdict: PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("fast output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("fast scoped run must not run the unchanged ./b full-only failing test:\n%s", out)
	}
}

// TestVerifyFastNoChangedPackagesBuildOnly pins the fast tier's build-only
// fallback: with a clean tree (no changed Go packages) the test step is
// dropped entirely and the note says so — never a silent empty test run. The
// committed full-only failure must not run (verdict stays PASS).
func TestVerifyFastNoChangedPackagesBuildOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setVerifyExecEnv(t)
	dir := verifyTiersFixture(t) // clean tree vs HEAD
	out, err := Verify(context.Background(), verifyHooks(dir), map[string]any{"root": dir, "types": "build,test", "fast": true})
	if err != nil {
		t.Fatalf("Verify(fast=true, clean tree): %v", err)
	}
	for _, want := range []string{"no changed Go packages — running build only", "verdict: PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("build-only output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tests:") {
		t.Errorf("fast with no changed packages must be build-only (no test step), got:\n%s", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("build-only run must not run the full-only failing test:\n%s", out)
	}
}

// TestVerifyFullRunsCompleteSuite pins the full tier: full=true passes the
// FullTests option so the COMPLETE suite runs — the committed short-skipping
// test runs (not skipped) and FAILs, proving -short was NOT used.
func TestVerifyFullRunsCompleteSuite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setVerifyExecEnv(t)
	dir := verifyTiersFixture(t)
	out, err := Verify(context.Background(), verifyHooks(dir), map[string]any{"root": dir, "types": "build,test", "full": true})
	if err != nil {
		t.Fatalf("Verify(full=true): %v", err)
	}
	if !strings.Contains(out, "full suite (complete test run)") {
		t.Errorf("full output missing the full-suite note:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("full suite must run the short-skipping test to failure (FullTests reached the engine):\n%s", out)
	}
}

// TestVerifyFullBeatsFast pins the interaction rule: fast=true + full=true is
// a --full run — the full-suite note prints, the fast mode/scoping notes do
// not (even with uncommitted changes present), and the complete suite runs
// (the short-skipping test fails).
func TestVerifyFullBeatsFast(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setVerifyExecEnv(t)
	dir := verifyTiersFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "a", "extra.go"), []byte("package a\n\nfunc Extra() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Verify(context.Background(), verifyHooks(dir), map[string]any{"root": dir, "types": "build,test", "fast": true, "full": true})
	if err != nil {
		t.Fatalf("Verify(fast=true, full=true): %v", err)
	}
	if !strings.Contains(out, "full suite (complete test run)") {
		t.Errorf("full beats fast: missing the full-suite note:\n%s", out)
	}
	for _, banned := range []string{"fast mode", "changed packages", "no changed Go packages"} {
		if strings.Contains(out, banned) {
			t.Errorf("full beats fast: fast-mode note %q must not appear:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("full beats fast: the complete suite must run the short-skipping test to failure:\n%s", out)
	}
}
