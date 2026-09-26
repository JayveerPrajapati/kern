package validate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if c.Cmd != "go" || len(c.Args) == 0 || c.Args[0] != "build" {
		t.Fatalf("expected go build, got %v %v", c.Cmd, c.Args)
	}
}

func TestDetectPython(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if c.Cmd != "python" && c.Cmd != "python3" {
		t.Fatalf("expected python, got %v", c.Cmd)
	}
}

func TestDetectUnknown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestDetectMissingToolchain(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// go binary is present on dev machines; assert Detect returns a command
	// with Cmd "go" or skips cleanly if unavailable.
	c, err := Detect(root)
	if err != nil {
		t.Logf("go not on PATH, skipping: %v", err)
		return
	}
	if c.Cmd != "go" {
		t.Fatalf("expected go, got %v", c.Cmd)
	}
}

func TestRunTrueCommand(t *testing.T) {
	c := &Command{Name: "true", Cmd: "true", Args: nil}
	res := Run(context.Background(), t.TempDir(), c, 10*time.Second)
	if !res.OK {
		t.Fatalf("true should pass: %+v", res)
	}
}

func TestRunFailingCommand(t *testing.T) {
	c := &Command{Name: "false", Cmd: "false", Args: nil}
	res := Run(context.Background(), t.TempDir(), c, 10*time.Second)
	if res.OK {
		t.Fatal("false should fail")
	}
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit code")
	}
}

func TestRunTimeout(t *testing.T) {
	c := &Command{Name: "sleep", Cmd: "sleep", Args: []string{"10"}}
	res := Run(context.Background(), t.TempDir(), c, 200*time.Millisecond)
	if res.OK {
		t.Fatal("sleep should time out")
	}
	if res.Err == nil {
		t.Fatal("expected timeout error")
	}
	// A signal-killed timeout must report exit -1, not the zero value 0
	// which is indistinguishable from a clean success (the reporting bug
	// that printed "exit 0" for a timed-out Maven build).
	if res.ExitCode != -1 {
		t.Fatalf("timeout exit code = %d, want -1", res.ExitCode)
	}
}

func TestDetectGoProjectRuns(t *testing.T) {
	// Exercise a real go vet run from this package directory (no network,
	// deps are local/cached).
	c := &Command{Name: "go vet (meta)", Cmd: "go", Args: []string{"vet", "./..."}}
	res := Run(context.Background(), ".", c, 60*time.Second)
	if !res.OK {
		t.Fatalf("go vet failed: %s", res.Output)
	}
}

// TestDetectKind verifies kind-filtered detection: a Go module resolves the
// test kind to `go test` (not the higher-priority build command), an npm
// project to `npm test`, and an empty directory errors.
func TestDetectKind(t *testing.T) {
	// The kern repo itself is a Go module.
	c, err := DetectKind("../..", "test")
	if err != nil {
		t.Fatalf("DetectKind(test) on go module: %v", err)
	}
	if c.Cmd != "go" || c.Kind != "test" {
		t.Fatalf("DetectKind(test) = %+v; want go test", c)
	}
	b, err := DetectKind("../..", "build")
	if err != nil {
		t.Fatalf("DetectKind(build) on go module: %v", err)
	}
	if b.Cmd != "go" || b.Kind != "build" {
		t.Fatalf("DetectKind(build) = %+v; want go build", b)
	}
	l, err := DetectKind("../..", "lint")
	if err != nil {
		t.Fatalf("DetectKind(lint) on go module: %v", err)
	}
	if l.Cmd != "go" || l.Kind != "lint" {
		t.Fatalf("DetectKind(lint) = %+v; want go vet", l)
	}

	// npm project.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectKind(root, "test"); err != nil {
		t.Skipf("npm not on PATH: %v", err)
	}
	c, err = DetectKind(root, "test")
	if err != nil || c.Cmd != "npm" || c.Kind != "test" {
		t.Fatalf("DetectKind(test) on npm project = (%+v, %v); want npm test", c, err)
	}

	// Empty directory: nothing detected.
	empty := t.TempDir()
	if _, err := DetectKind(empty, "test"); err == nil {
		t.Fatal("DetectKind on empty dir should error")
	}
}

// TestRunChecksBrokenGoFailsWithoutToolchain pins the D4 regression: a broken
// .go file next to a broken .py must FAIL per-extension validation — the Go
// syntax check is the deterministic in-process go/parser parse and runs even
// when no toolchain would be detected (the old auto-detect validated only the
// winning language, letting the broken .go pass as "OK").
func TestRunChecksBrokenGoAndPyFail(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package main\n\nfunc broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.py"), []byte("def broken(\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunChecks(context.Background(), root, "", 30*time.Second)
	if res.Err != nil {
		t.Fatalf("RunChecks: %v", res.Err)
	}
	if res.OK {
		t.Fatal("broken .go must fail validation (D4 regression)")
	}
	if !contains(res.FailedChecks(), "go syntax") {
		t.Fatalf("expected go syntax to fail, failed=%v skipped=%v", res.FailedChecks(), res.SkippedChecks())
	}
	if !strings.Contains(res.Output, "broken.go:") {
		t.Fatalf("output must name broken.go with a line, got:\n%s", res.Output)
	}
	if !pythonOnPath(t) {
		t.Skip("python not on PATH; python half of the check skipped")
	}
	if !contains(res.FailedChecks(), "python py_compile") {
		t.Fatalf("expected python py_compile to fail, failed=%v", res.FailedChecks())
	}
	if !strings.Contains(res.Output, "broken.py:") {
		t.Fatalf("output must name broken.py (normalized compileall), got:\n%s", res.Output)
	}
}

// TestRunChecksHealthyPolyglotPasses verifies healthy Go + Python files pass
// per-extension validation.
func TestRunChecksHealthyPolyglotPasses(t *testing.T) {
	if !pythonOnPath(t) {
		t.Skip("python not on PATH")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.py"), []byte("def ok():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunChecks(context.Background(), root, "", 30*time.Second)
	if res.Err != nil {
		t.Fatalf("RunChecks: %v", res.Err)
	}
	if !res.OK {
		t.Fatalf("healthy files must pass, failed=%v skipped=%v\n%s", res.FailedChecks(), res.SkippedChecks(), res.Output)
	}
}

// TestRunChecksFileScoping verifies heal --file semantics: RunChecks with a
// file filter only validates that file — the sibling broken .py is neither
// checked nor named in the output.
func TestRunChecksFileScoping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package main\n\nfunc broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.py"), []byte("def broken(\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunChecks(context.Background(), root, "broken.go", 30*time.Second)
	if res.Err != nil {
		t.Fatalf("RunChecks: %v", res.Err)
	}
	if res.OK {
		t.Fatal("scoped to broken.go it must fail")
	}
	for _, c := range res.Checks {
		if c.Name == "python py_compile" {
			t.Fatal("python check must not run when scoped to broken.go")
		}
	}
	if !strings.Contains(res.Output, "broken.go:") {
		t.Fatalf("scoped output must name broken.go, got:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "broken.py") {
		t.Fatalf("scoped output must not mention broken.py, got:\n%s", res.Output)
	}

	// The scoped run must pass once the file is healthy.
	_ = os.WriteFile(filepath.Join(root, "broken.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	res2 := RunChecks(context.Background(), root, "broken.go", 30*time.Second)
	if res2.Err != nil {
		t.Fatalf("RunChecks: %v", res2.Err)
	}
	if !res2.OK {
		t.Fatalf("healthy scoped file must pass, failed=%v skipped=%v\n%s", res2.FailedChecks(), res2.SkippedChecks(), res2.Output)
	}
}

// TestRunChecksUnvalidatableLangNotOK verifies the "unable to validate"
// fallback: a language with no syntax checker must NOT read as OK.
func TestRunChecksUnvalidatableLangNotOK(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunChecks(context.Background(), root, "", 10*time.Second)
	if res.Err != nil {
		t.Fatalf("RunChecks: %v", res.Err)
	}
	if res.OK {
		t.Fatal("unvalidatable language must not read as OK")
	}
	if len(res.FailedChecks()) != 0 {
		t.Fatalf("nothing should fail, failed=%v", res.FailedChecks())
	}
	if len(res.SkippedChecks()) == 0 {
		t.Fatalf("expected skipped checks for rust, got %+v", res.Checks)
	}
}

func pythonOnPath(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("python"); err == nil {
		return true
	}
	_, err := exec.LookPath("python3")
	return err == nil
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// TestDetectNoModuleGoVetPerFile pins F1 in validate: a root with loose .go
// files and NO go.mod must offer a vet command that targets the explicit
// files — `go vet ./...` is invalid outside a module ("directory prefix .
// does not contain main module"), so the no-module candidate must never
// carry the ./... pattern.
func TestDetectNoModuleGoVetPerFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stray.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package main\nvar _ = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect on no-module go root: %v", err)
	}
	if c.Cmd != "go" {
		t.Fatalf("expected go, got %v", c.Cmd)
	}
	if len(c.Args) < 2 || c.Args[0] != "vet" {
		t.Fatalf("expected go vet <files>, got %v %v", c.Cmd, c.Args)
	}
	for _, a := range c.Args {
		if strings.Contains(a, "./...") {
			t.Errorf("no-module vet candidate must not use ./...: %v", c.Args)
		}
	}
	// The explicit file names must be present.
	joined := strings.Join(c.Args, " ")
	if !strings.Contains(joined, "stray.go") || !strings.Contains(joined, "other.go") {
		t.Errorf("vet candidate must name the root .go files: %v", c.Args)
	}
	// It must also be the lint-kind candidate.
	l, err := DetectKind(root, "lint")
	if err != nil {
		t.Fatalf("DetectKind(lint) on no-module go root: %v", err)
	}
	if l.Cmd != "go" || l.Kind != "lint" {
		t.Fatalf("DetectKind(lint) = %+v; want go vet", l)
	}
}
