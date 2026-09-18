package calibrate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/version"
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

// TestCacheWarmRerun: a second Run over the same (root, commit-range,
// thresholds) tuple must replay the stored report with the "cached" marker
// line, byte-identical to the fresh report.
func TestCacheWarmRerun(t *testing.T) {
	dir := tinyGitRepo(t)

	var first bytes.Buffer
	if err := Run(dir, 2, []float64{2.0, 4.0}, &first); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if strings.Contains(first.String(), "cached") {
		t.Fatalf("first run must not be served from cache:\n%s", first.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "calibrate-cache.json")); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	var second bytes.Buffer
	if err := Run(dir, 2, []float64{2.0, 4.0}, &second); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	secondOut := second.String()
	if !strings.HasPrefix(secondOut, "# cached calibrate result") {
		t.Fatalf("warm rerun missing cached marker:\n%s", secondOut)
	}
	report := strings.TrimPrefix(secondOut, secondOut[:strings.IndexByte(secondOut, '\n')+1])
	if report != first.String() {
		t.Fatalf("cached report differs from fresh report:\n--- fresh ---\n%s\n--- cached ---\n%s", first.String(), report)
	}
}

// TestCacheMissOnChangedInputs: a different threshold sweep (or window) is a
// different key and must recompute, not replay the previous entry.
func TestCacheMissOnChangedInputs(t *testing.T) {
	dir := tinyGitRepo(t)

	var a bytes.Buffer
	if err := Run(dir, 2, []float64{2.0, 4.0}, &a); err != nil {
		t.Fatalf("Run thresholds 2,4: %v", err)
	}

	var b bytes.Buffer
	if err := Run(dir, 2, []float64{6.0, 8.0}, &b); err != nil {
		t.Fatalf("Run thresholds 6,8: %v", err)
	}
	if strings.Contains(b.String(), "cached") {
		t.Fatalf("different thresholds must miss the cache:\n%s", b.String())
	}

	// Original tuple still served from cache afterwards.
	var c bytes.Buffer
	if err := Run(dir, 2, []float64{2.0, 4.0}, &c); err != nil {
		t.Fatalf("Run thresholds 2,4 again: %v", err)
	}
	if !strings.HasPrefix(c.String(), "# cached calibrate result") {
		t.Fatalf("original tuple should hit cache after eviction-less reuse:\n%s", c.String())
	}
}

// TestCacheMissOnVersionChange: the cache key includes the engine version
// (internal/version.Version), so a different version — as a release bump
// would stamp — must miss the cache even with an identical (root, range,
// thresholds) tuple.
func TestCacheMissOnVersionChange(t *testing.T) {
	dir := tinyGitRepo(t)
	orig := version.Version
	defer func() { version.Version = orig }()

	var a bytes.Buffer
	if err := Run(dir, 2, []float64{2.0}, &a); err != nil {
		t.Fatalf("Run v1: %v", err)
	}

	version.Version = "9.9.9-test"
	var b bytes.Buffer
	if err := Run(dir, 2, []float64{2.0}, &b); err != nil {
		t.Fatalf("Run v2: %v", err)
	}
	if strings.Contains(b.String(), "cached") {
		t.Fatalf("different engine version must miss the cache:\n%s", b.String())
	}

	// Restored version hits again.
	version.Version = orig
	var c bytes.Buffer
	if err := Run(dir, 2, []float64{2.0}, &c); err != nil {
		t.Fatalf("Run v1 again: %v", err)
	}
	if !strings.HasPrefix(c.String(), "# cached calibrate result") {
		t.Fatalf("original version should hit cache after restore:\n%s", c.String())
	}
}
