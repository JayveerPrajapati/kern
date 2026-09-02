package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// calibrateCLIFixture builds a temp git checkout with two commits (baseline
// then a change to main.go) so `kern calibrate` has history to score.
func calibrateCLIFixture(t *testing.T) string {
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
	write("go.mod", "module calibrateclitest\n\ngo 1.20\n")
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
	write("main.go", `package main

func helper() string { return "h2" }

func main() { _ = helper() }
`)
	git("add", ".")
	git("commit", "-m", "change helper")
	return dir
}

// TestCalibrateCommand exercises runCalibrate in-process: the positional
// root is the fixture dir and the report must include the impact-F1 section.
func TestCalibrateCommand(t *testing.T) {
	dir := calibrateCLIFixture(t)
	out := captureStdout(t, func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(exitError); ok {
					t.Fatalf("runCalibrate exited with code %d", e.code)
				}
				panic(r)
			}
		}()
		runCalibrate([]string{dir, "--commits", "2", "--thresholds", "2.0,4.0"})
	})
	if !strings.Contains(out, "impact F1") {
		t.Fatalf("output missing impact F1 header:\n%s", out)
	}
}
