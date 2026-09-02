package calibrate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tinyGitRepo builds a temp git checkout with two commits so the harness has
// history to score: commit A (baseline) adds a Go module whose main.go defines
// helper() and whose main_test.go calls it; commit B modifies helper() in
// main.go. The call graph can then predict the test file as part of the
// helper's blast radius, giving the impact-F1 protocol real signals.
func tinyGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.name", "kern calibrate test")
	git("config", "user.email", "kern-calibrate@test.local")
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("go.mod", "module calibratetest\n\ngo 1.20\n")
	write("main.go", `package main

func helper() string { return "h" }

func main() { _ = helper() }
`)
	write("main_test.go", `package main

import "testing"

func TestHelper(t *testing.T) { if helper() != "h" { t.Fatal("bad") } }
`)
	git("add", ".")
	git("commit", "-m", "baseline")
	// Commit B: change the helper so the commit touches main.go again.
	write("main.go", `package main

func helper() string { return "h2" }

func main() { _ = helper() }
`)
	git("add", ".")
	git("commit", "-m", "change helper")
	return dir
}

// TestRunOnTinyGitRepo runs the harness against a two-commit fixture: Run
// builds the index itself (load-or-build like the standalone main) and must
// report the impact-F1 section.
func TestRunOnTinyGitRepo(t *testing.T) {
	dir := tinyGitRepo(t)
	var buf bytes.Buffer
	if err := Run(dir, 2, []float64{2.0, 4.0}, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "impact F1") {
		t.Fatalf("output missing impact F1 header:\n%s", out)
	}
	if !strings.Contains(out, "precision=") {
		t.Fatalf("output missing precision= line:\n%s", out)
	}
}

// TestRunErrorsOnNonGitDir: without git history the rev-list step fails and
// Run must surface the error.
func TestRunErrorsOnNonGitDir(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := Run(dir, 2, []float64{2.0}, &buf); err == nil {
		t.Fatal("Run on a non-git dir: expected error, got nil")
	}
}
