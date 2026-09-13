package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixesExitCode runs fn and returns the exit code it raised through the
// exitError sentinel panic (0 when fn returns normally), mirroring the
// recover/exit loop in production main(). Tests for F-006/F-024/F-033 use it
// to observe usage/runtime exit codes of runSearch/runPrompt/runPath.
func fixesExitCode(fn func()) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	fn()
	return 0
}

// fixesRunStderrExit runs fn with os.Stderr redirected to a pipe and returns
// the captured stderr plus the exit code raised through the exitError
// sentinel panic (0 when fn returns normally). Unlike captureStderr it drains
// the pipe even when fn panics, so stderr written by fatal()/fatalUsage()
// before the panic is observable in-process (F-024).
func fixesRunStderrExit(fn func()) (stderr string, code int) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stderr = w
	defer func() {
		_ = w.Close()
		os.Stderr = old
		if b, rerr := io.ReadAll(r); rerr == nil {
			stderr = string(b)
		}
		if rp := recover(); rp != nil {
			if e, ok := rp.(exitError); ok {
				code = e.code
				return
			}
			panic(rp)
		}
	}()
	fn()
	return "", 0
}

// fixesChdir moves the test process into dir for the duration of fn and
// restores the previous working directory afterwards. cmd/kern tests do not
// run in parallel, so chdir is safe here.
func fixesChdir(t *testing.T, dir string, fn func()) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

// TestRunSearchMultiWordQueryJoinsPositionals (F-006): `kern search user
// service --limit 5` must join both positional words into ONE query and
// search the current repo. Before the fix the 2nd token was taken as the
// repo root, so the search failed with `lstat <cwd>/service: no such file
// or directory` (exit 1) whenever the word was not an existing directory.
func TestRunSearchMultiWordQueryJoinsPositionals(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module searchfix\n\ngo 1.20\n")
	writeFixtureFile(t, dir, "service.go", `package main

// UserService handles user provisioning.
func UserService() int { return 1 }

func main() { _ = UserService() }
`)
	fixesChdir(t, dir, func() {
		var out string
		code := fixesExitCode(func() {
			out = captureStdout(t, func() {
				runSearch([]string{"user", "service", "--limit", "5"})
			})
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (multi-word query must not lstat a fake root)", code)
		}
		if !strings.Contains(out, "UserService") {
			t.Errorf("joined query \"user service\" did not surface UserService; got:\n%s", out)
		}
	})
}

// TestRunSearchExistingDirRootStillHonored (F-006): `kern search FindUser
// /existing/repo` must keep treating the trailing positional that names an
// existing directory as the repo root and search only the leading word.
func TestRunSearchExistingDirRootStillHonored(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module searchfix\n\ngo 1.20\n")
	writeFixtureFile(t, dir, "user.go", `package main

// FindUser looks up a user.
func FindUser() int { return 1 }

func main() { _ = FindUser() }
`)
	var out string
	code := fixesExitCode(func() {
		out = captureStdout(t, func() {
			runSearch([]string{"FindUser", dir, "--limit", "5"})
		})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (existing-dir root must still be honored)", code)
	}
	if !strings.Contains(out, "FindUser") {
		t.Errorf("root-scoped search did not surface FindUser; got:\n%s", out)
	}
}

// TestRunPromptUnknownTemplateGuidesUser (F-024): `kern prompt "Fix this:
// {{task}}"` must fail with a self-diagnosing error — the available template
// names plus the --file PATH escape hatch — instead of a bare
// `unknown template "Fix this: {{task}}"`.
func TestRunPromptUnknownTemplateGuidesUser(t *testing.T) {
	// Run from a scratch dir so the pre-render project-map build is cheap.
	dir := t.TempDir()
	fixesChdir(t, dir, func() {
		stderr, code := fixesRunStderrExit(func() {
			runPrompt([]string{"Fix this: {{task}}", "--task", "X"})
		})
		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (unknown template)", code)
		}
		if !strings.Contains(stderr, "unknown template") {
			t.Errorf("stderr missing unknown-template note: %s", stderr)
		}
		if !strings.Contains(stderr, "available templates") {
			t.Errorf("stderr missing template list heading: %s", stderr)
		}
		if !strings.Contains(stderr, "kern prompt list") {
			t.Errorf("stderr missing `kern prompt list` hint: %s", stderr)
		}
		if !strings.Contains(stderr, "--file PATH") {
			t.Errorf("stderr missing --file PATH hint: %s", stderr)
		}
	})
}

// TestRunPathFromToFlagAliases (F-033): `kern path --from A --to B` must be
// a flag alias for the positional form `kern path A B [root]` — both render
// the same path.
func TestRunPathFromToFlagAliases(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module pathfix\n\ngo 1.20\n")
	writeFixtureFile(t, dir, "demo.go", `package main

// Helper returns a string.
func Helper() string { return "h" }

func main() { _ = Helper() }
`)
	var outFlags, outPos string
	codeFlags := fixesExitCode(func() {
		outFlags = captureStdout(t, func() {
			runPath([]string{"--from", "main", "--to", "Helper", "--root", dir})
		})
	})
	if codeFlags != 0 {
		t.Fatalf("flag form exit code = %d, want 0 (stderr above)", codeFlags)
	}
	codePos := fixesExitCode(func() {
		outPos = captureStdout(t, func() {
			runPath([]string{"main", "Helper", dir})
		})
	})
	if codePos != 0 {
		t.Fatalf("positional form exit code = %d, want 0 (stderr above)", codePos)
	}
	for name, out := range map[string]string{"flag form": outFlags, "positional form": outPos} {
		if !strings.Contains(out, "Helper") {
			t.Errorf("%s did not render a path to Helper: %s", name, out)
		}
		if strings.Contains(out, "no path found") {
			t.Errorf("%s reported no path main -> Helper: %s", name, out)
		}
	}
}

// TestRunPathFromToFlagsRequireBoth (F-033): passing only one of --from/--to
// is a usage error (exit 2), matching the two-required-positionals contract.
func TestRunPathFromToFlagsRequireBoth(t *testing.T) {
	_, code := fixesRunStderrExit(func() {
		runPath([]string{"--from", "main", filepath.Join(t.TempDir(), "root")})
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error for a lone --from)", code)
	}
}
