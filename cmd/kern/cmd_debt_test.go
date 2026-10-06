package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// debtFixture builds a git repo with two packages importing each other (a
// package cycle) and one fix commit touching them, so both debt engines have
// something to report. Global git hooks are neutralized (core.hooksPath=).
func debtFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=", "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/debt\n\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pkg", "a", "a.go"),
		[]byte("package a\n\nimport \"example.com/debt/pkg/b\"\n\nfunc A() { b.B() }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pkg", "b", "b.go"),
		[]byte("package b\n\nimport \"example.com/debt/pkg/a\"\n\nfunc B() { a.A() }\n"), 0o644)
	git("init")
	git("add", ".")
	git("commit", "-m", "initial")
	os.WriteFile(filepath.Join(dir, "pkg", "a", "a.go"),
		[]byte("package a\n\nimport \"example.com/debt/pkg/b\"\n\nfunc A() { b.B(); b.B() }\n"), 0o644)
	git("add", ".")
	git("commit", "-m", "fix: duplicate call in A")
	return dir
}

func TestRunDebtJSON(t *testing.T) {
	root := debtFixture(t)
	out := captureStdout(t, func() { runDebt([]string{"--json", root}) })
	var rep debtReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rep); err != nil {
		t.Fatalf("debt --json not valid JSON: %v\n%s", err, out)
	}
	if len(rep.Cycles) == 0 {
		t.Fatal("expected at least one import cycle in fixture")
	}
	joined := strings.Join(rep.Cycles[0].Packages, ",")
	if !strings.Contains(joined, "pkg/a") || !strings.Contains(joined, "pkg/b") {
		t.Fatalf("cycle packages missing fixture packages: %v", rep.Cycles[0].Packages)
	}
	if len(rep.Hotspots) == 0 {
		t.Fatal("expected at least one fragility hotspot from the fix commit")
	}
}

func TestRunDebtHuman(t *testing.T) {
	root := debtFixture(t)
	out := captureStdout(t, func() { runDebt([]string{root}) })
	for _, want := range []string{"Technical Debt Report", "Import Cycles", "pkg/a", "hotspots"} {
		if !strings.Contains(out, want) {
			t.Errorf("debt human output missing %q:\n%s", want, out)
		}
	}
}

func TestRunDebtNonGitDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/nogit\n\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	out := captureStdout(t, func() { runDebt([]string{dir}) })
	if !strings.Contains(out, "Technical Debt Report") {
		t.Fatalf("debt must degrade gracefully on a non-git root:\n%s", out)
	}
	if strings.Contains(out, "panic") {
		t.Fatal("panic text in output")
	}
}
