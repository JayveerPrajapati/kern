package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
	out, fatal := captureVerifyOutput(t, func() { runVerify([]string{"--changed-since", baseRef, "--root", dir}) })
	if fatal {
		t.Fatalf("--changed-since scoped run must PASS, runVerify fatal'd; inner report:\n%s", out)
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

// captureVerifyOutput runs fn with os.Stdout captured. runVerify's fatal()
// panics with exitError; instead of letting it crash the test binary (and
// losing the captured report with the pipe), it is recovered so the caller
// can assert on — and dump — the inner verification report.
func captureVerifyOutput(t *testing.T, fn func()) (out string, fatal bool) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if _, ok := rec.(exitError); !ok {
					panic(rec)
				}
				fatal = true
			}
		}()
		fn()
	}()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), fatal
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
	out := captureStdout(t, func() {
		code = dispatchCommand("verify", []string{"--changed-since", "HEAD", "--root", dir})
	})
	if code != 0 {
		t.Fatalf("dispatchCommand(verify --changed-since HEAD) = %d, want 0 (clean diff)", code)
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
		stderr, code = captureStderrExit(t, func() {
			runVerify([]string{"--changed-since", "no-such-ref-xyz", "--root", dir})
		})
	})
	if code != 2 {
		t.Fatalf("runVerify --changed-since <invalid-ref> = exit %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "invalid git ref") {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, "invalid git ref")
	}
}
