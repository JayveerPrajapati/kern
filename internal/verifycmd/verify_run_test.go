package verifycmd

// verify_run_test.go carries the behavioral tests for the `kern verify` run
// family, moved from cmd/kern (workstream 3.6-A — verify family extraction):
// the F13 render-detail tests, the --fast tier tests and the --changed-since
// scoping tests now exercise the exported entry points (VerifyRun,
// VerifyTestsDetail) plus the internal helpers directly. The cmd/kern suite
// keeps only the genuine wiring around the dispatch table.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns the
// captured output — the cmd/kern test-helper pattern, local copy (the moved
// tests cannot reach package main's helpers).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns the
// captured output.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// F13: the default `kern verify` render shows counts plus the FAILING tests'
// excerpts only — passing-test spam is suppressed and a pointer to the audit
// log (which holds the full output) is printed whenever one was written.
func TestVerifyTestsDetailShowsFailuresOnly(t *testing.T) {
	out := `=== RUN   TestAddPasses
--- PASS: TestAddPasses (0.00s)
=== RUN   TestAddPasses2
--- PASS: TestAddPasses2 (0.00s)
=== RUN   TestAddFails
    add_test.go:9: Add(1,1) unexpectedly equals 2
--- FAIL: TestAddFails (0.00s)
FAIL
FAIL	fix13	1.159s
`
	tr := &verdict.TestResult{OK: false, Output: out, LogPath: ".kern/audit/20261004-090000.000000001/verify-test.log"}
	lines := VerifyTestsDetail(tr)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "--- FAIL: TestAddFails") {
		t.Fatalf("failing test marker must be shown, got: %s", joined)
	}
	if !strings.Contains(joined, "add_test.go:9: Add(1,1) unexpectedly equals 2") {
		t.Fatalf("failing test diagnostic must be shown, got: %s", joined)
	}
	if strings.Contains(joined, "--- PASS") || strings.Contains(joined, "=== RUN") {
		t.Fatalf("passing-test spam must be suppressed, got: %s", joined)
	}
	if !strings.Contains(joined, "full log: .kern/audit/20261004-090000.000000001/verify-test.log") {
		t.Fatalf("full-output pointer must be present, got: %s", joined)
	}
}

// A green run prints no test output at all (passing spam suppressed); the
// pointer remains so the full log is still reachable.
func TestVerifyTestsDetailGreenRunSuppressesOutput(t *testing.T) {
	out := `=== RUN   TestAddPasses
--- PASS: TestAddPasses (0.00s)
PASS
ok  	fix13	1.159s
`
	tr := &verdict.TestResult{OK: true, Output: out, LogPath: ".kern/audit/20261004-090000.000000002/verify-test.log"}
	lines := VerifyTestsDetail(tr)

	if len(lines) != 1 || !strings.HasPrefix(lines[0], "full log: ") {
		t.Fatalf("green run must print only the full-log pointer, got: %q", lines)
	}
}

// No audit log written (write failed or empty run): no pointer, no
// fabricated path.
func TestVerifyTestsDetailNoLogNoPointer(t *testing.T) {
	tr := &verdict.TestResult{OK: true, Output: "--- PASS: TestX (0.00s)\n"}
	if lines := VerifyTestsDetail(tr); len(lines) != 0 {
		t.Fatalf("no LogPath must mean no pointer, got: %q", lines)
	}
}

// A large failure is line-bounded with an honest more-lines note.
func TestTestFailureExcerptBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("--- FAIL: TestMany\n")
	}
	got := verdict.TestFailureExcerpt(b.String(), 40)
	if n := strings.Count(got, "--- FAIL"); n != 40 {
		t.Fatalf("excerpt must be bounded to 40 lines, got %d", n)
	}
	if !strings.Contains(got, "... 60 more lines in the full log") {
		t.Fatalf("bounded excerpt must say how many lines were cut, got: %s", got)
	}
}

// A non-Go runner's output has no go-test noise prefixes, so the excerpt
// keeps it verbatim (still bounded) — never silently empty.
func TestTestFailureExcerptNonGoRunnerKept(t *testing.T) {
	got := verdict.TestFailureExcerpt("1 failed, 3 passed in 0.5s\nE   AssertionError: boom\n", 40)
	if !strings.Contains(got, "E   AssertionError: boom") {
		t.Fatalf("non-Go failure output must be kept, got: %s", got)
	}
}

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
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--fast", "--root", dir}) })
	if code != 0 {
		t.Fatalf("--fast build-only run must exit 0, got %d", code)
	}
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
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--fast", "--root", dir}) })
	if code != 0 {
		t.Fatalf("--fast changed-scoped run must exit 0, got %d", code)
	}
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
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--fast", "--full", "--root", dir}) })
	if code != 0 {
		t.Fatalf("--fast --full run must exit 0, got %d", code)
	}
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

// changedSinceFixture builds a tiny Go module in a git repo with a base
// commit and a second commit that modifies a/a.go; it returns the repo dir
// and the base commit's short hash. postChange=true additionally leaves an
// uncommitted modification in b/b.go and an untracked c/c.go, so the
// --changed-since derivation covers committed, uncommitted and untracked
// changes in one sweep.
//
// EVERY fixture git command runs with -c core.hooksPath= so global hooks on
// the host machine (this dev box has some) can never fire inside the
// fixture and fail the test.
func changedSinceFixture(t *testing.T, postChange bool) (dir, baseRef string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath="}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return string(out)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write("go.mod", "module example.com/m\n\ngo 1.23\n")
	write("a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	baseRef = strings.TrimSpace(git("rev-parse", "--short", "HEAD"))
	// second commit: a committed change in a/ since the base ref
	write("a/a.go", "package a\n\nfunc A() int { return 42 }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "second")
	if postChange {
		// uncommitted change in b/ + untracked file in c/
		write("b/b.go", "package b\n\nfunc B() int { return 22 }\n")
		write("c/c.go", "package c\n\nfunc C() int { return 3 }\n")
	}
	return dir, baseRef
}

// TestChangedSincePackages pins the --changed-since package derivation: the
// UNION of committed changes after the ref, uncommitted changes and
// untracked files maps to package dirs (root files → ./).
func TestChangedSincePackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, baseRef := changedSinceFixture(t, true)
	pkgs, err := changedSincePackages(dir, baseRef)
	if err != nil {
		t.Fatalf("changedSincePackages: %v", err)
	}
	want := []string{"./a", "./b", "./c"}
	if !reflect.DeepEqual(pkgs, want) {
		t.Fatalf("changedSincePackages(%q) = %v, want %v", baseRef, pkgs, want)
	}
	// No changes since HEAD → empty (NO-GO-CHANGES path). Uses the clean
	// fixture (postChange=false): the postChange=true tree above has
	// uncommitted b/c changes that WOULD show up against HEAD.
	cleanDir, _ := changedSinceFixture(t, false)
	if pkgs, err := changedSincePackages(cleanDir, "HEAD"); err != nil || len(pkgs) != 0 {
		t.Fatalf("changedSincePackages(HEAD) = %v, %v; want empty, nil", pkgs, err)
	}
}

// TestChangedSincePackagesEdgeCases pins the per-edge-case behavior:
// root-package files map to ./; testdata/ paths are skipped; a deleted .go
// file's package is dropped (its files no longer exist on disk); untracked
// files still count.
func TestChangedSincePackagesEdgeCases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath="}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return string(out)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write("go.mod", "module example.com/m\n\ngo 1.23\n")
	write("main.go", "package main\n\nfunc main() {}\n")                   // root package
	write("a/a.go", "package a\n\nfunc A() int { return 1 }\n")            // modified → ./a
	write("x/x.go", "package x\n\nfunc X() int { return 1 }\n")            // deleted → dropped
	write("testdata/fix.go", "package fix\n\nfunc F() int { return 1 }\n") // testdata → skipped
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	baseRef := strings.TrimSpace(git("rev-parse", "--short", "HEAD"))

	// working-tree changes since baseRef:
	write("main.go", "package main\n\nfunc main() { _ = 1 }\n")            // root package change
	write("a/a.go", "package a\n\nfunc A() int { return 2 }\n")            // package a change
	write("testdata/fix.go", "package fix\n\nfunc F() int { return 2 }\n") // testdata — skipped
	if err := os.Remove(filepath.Join(dir, "x", "x.go")); err != nil {     // deleted — dropped
		t.Fatal(err)
	}
	write("z/z.go", "package z\n\nfunc Z() int { return 3 }\n") // untracked → ./z

	pkgs, err := changedSincePackages(dir, baseRef)
	if err != nil {
		t.Fatalf("changedSincePackages: %v", err)
	}
	want := []string{"./", "./a", "./z"}
	if !reflect.DeepEqual(pkgs, want) {
		t.Fatalf("changedSincePackages(%q) = %v, want %v", baseRef, pkgs, want)
	}
}

// TestRunVerifyChangedSinceScopesChangedPackages pins the end-to-end
// --changed-since path: the printed scoping line names exactly the packages
// changed since the ref (committed + uncommitted + untracked), and the
// scoped verification (build + vet + tests) passes.
func TestRunVerifyChangedSinceScopesChangedPackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	// KERN_ALLOW_UNISOLATED: the inner engine's test stage needs network
	// isolation for its sandbox; when this test itself runs inside the outer
	// `kern verify` test stage (already sandboxed), a nested sandbox cannot
	// be applied and the stage is skipped with "network isolation
	// unavailable" — which the --changed-since verdict treats as FAIL (it
	// requires test + vet). Running the fixture's tests unisolated is safe
	// (t.TempDir fixture) and keeps the test green both standalone and
	// nested.
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, baseRef := changedSinceFixture(t, true)
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--changed-since", baseRef, "--root", dir}) })
	if code != 0 {
		t.Fatalf("--changed-since scoped run must PASS, VerifyRun exit %d; inner report:\n%s", code, out)
	}
	for _, want := range []string{
		"changed packages since " + baseRef + " (3): ./a ./b ./c",
		"build: OK",
		"verdict: PASS",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--changed-since output missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("--changed-since scoped run must not FAIL, got:\n%s", out)
	}
}

// TestRunVerifyChangedSinceNoGoChanges pins the NO-GO-CHANGES contract: a
// ref with no Go changes since it prints "NO-GO-CHANGES since <ref> —
// nothing to verify" and exits 0 (a clean diff is a success for CI), never
// falling through to a full ./... run.
func TestRunVerifyChangedSinceNoGoChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, _ := changedSinceFixture(t, false) // HEAD == the second commit; clean tree
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--changed-since", "HEAD", "--root", dir}) })
	if code != 0 {
		t.Fatalf("VerifyRun(verify --changed-since HEAD) = %d, want 0 (clean diff)", code)
	}
	if !strings.Contains(out, "NO-GO-CHANGES since HEAD — nothing to verify") {
		t.Fatalf("output missing NO-GO-CHANGES line, got:\n%s", out)
	}
	for _, banned := range []string{"build:", "verdict:", "tests:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("NO-GO-CHANGES must short-circuit before verification (found %q), got:\n%s", banned, out)
		}
	}
}

// TestRunVerifyChangedSinceInvalidRefExits2 pins the invalid-ref contract:
// a ref git cannot resolve is a usage error (exit 2), never a silent "no
// changes" that falls through to the full suite.
func TestRunVerifyChangedSinceInvalidRefExits2(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, _ := changedSinceFixture(t, false)
	// The pre-branch mode note prints to stdout before the ref check; wrap
	// the run so only stderr (the fatalUsage error) is asserted on.
	var stderr string
	var code int
	_ = captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code = VerifyRun([]string{"--changed-since", "no-such-ref-xyz", "--root", dir})
		})
	})
	if code != 2 {
		t.Fatalf("VerifyRun --changed-since <invalid-ref> = exit %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "invalid git ref") {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, "invalid git ref")
	}
}

// TestRunVerifyCLINotExecGated pins the CLI-trusted-principal split (P3):
// the operator-invoked `kern verify` types path — including the machine-wide
// pre-commit hook's exact invocation `kern verify --staged
// security,architecture` — must NOT require the agent exec allowlist
// (KERN_ALLOW_EXEC / KERN_TOOLS). The governance gate lives at the MCP
// boundary (internal/mcp/highlevel) and in `--command` mode
// (verify_command.go), not on this CLI path. Regression for the
// commit-blocking defect: without the allowlist exported, the old gate here
// made EVERY git commit fail on machines with the global hook installed.
func TestRunVerifyCLINotExecGated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// The dev box exports KERN_ALLOW_EXEC globally; clear it plus the tool
	// allowlist so the OLD CLI gate would have refused deterministically.
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_EXEC_RISK", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath="}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return string(out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package m\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	// The hook's exact invocation: staged scope, index-only checks.
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--staged", "security,architecture", "--root", dir}) })
	if code != 0 {
		t.Fatalf("CLI verify --staged without KERN_ALLOW_EXEC must succeed (exit 0), got %d; output:\n%s", code, out)
	}
	if strings.Contains(out, "command execution blocked") {
		t.Fatalf("CLI verify must not hit the agent exec gate, output:\n%s", out)
	}
}

// TestRunVerifyStagedEmptyPrintsFallbackNotice pins the --staged empty-index
// notice: with NOTHING staged, the run says so explicitly instead of looking
// like a staged-diff gate — security and architecture are whole-tree checks,
// and the notice teaches both facts. With something staged the notice must
// NOT appear (that run is a genuine staged scope).
func TestRunVerifyStagedEmptyPrintsFallbackNotice(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := fastVerifyFixture(t, false) // clean tree: nothing changed, nothing staged
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--staged", "security", "--root", dir}) })
	if code != 0 {
		t.Fatalf("--staged empty-index run must exit 0 (WARN findings are exit 0), got %d", code)
	}
	if !strings.Contains(out, "nothing staged") {
		t.Fatalf("--staged with an empty index must print the nothing-staged notice, got:\n%s", out)
	}
}

// TestRunVerifyStagedNonEmptyOmitsFallbackNotice pins the other half of the
// notice contract: a real staged change suppresses the notice.
func TestRunVerifyStagedNonEmptyOmitsFallbackNotice(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := fastVerifyFixture(t, true)
	// Stage the change: the fixture leaves it as an uncommitted working-tree
	// edit (not staged), so add it to the index explicitly.
	cmd := exec.Command("git", "add", "a/a.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add a/a.go: %v (%s)", err, out)
	}
	var code int
	out := captureStdout(t, func() { code = VerifyRun([]string{"--staged", "security", "--root", dir}) })
	if code != 0 {
		t.Fatalf("--staged run with a staged change must exit 0, got %d", code)
	}
	if strings.Contains(out, "nothing staged") {
		t.Fatalf("--staged with a staged change must NOT print the nothing-staged notice, got:\n%s", out)
	}
}

// TestVerifySecurityDetailWarnLabel pins the honest render: a security check
// with findings but no unsuppressed critical is OK (non-blocking) — but the
// detail line must say WARN, never the self-contradictory "OK findings=N".
func TestVerifySecurityDetailWarnLabel(t *testing.T) {
	if got := securityStatusLabel(&verdict.SecurityResult{Count: 3, High: 3, OK: true}); got != "WARN" {
		t.Fatalf("findings present (non-blocking) must render WARN, got %s", got)
	}
	if got := securityStatusLabel(&verdict.SecurityResult{OK: true}); got != "OK" {
		t.Fatalf("clean scan must render OK, got %s", got)
	}
	if got := securityStatusLabel(&verdict.SecurityResult{Count: 1, Critical: 1, OK: false}); got != "FAIL" {
		t.Fatalf("blocking scan must render FAIL, got %s", got)
	}
}
