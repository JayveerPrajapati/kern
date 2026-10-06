package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubTask writes a minimal task fixture into dir and returns its path.
func stubTask(t *testing.T, dir, name string) string {
	t.Helper()
	td := filepath.Join(dir, name)
	fix := filepath.Join(td, "fixture")
	if err := os.MkdirAll(fix, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "task.md"), []byte("# Demo\n\nDo the thing."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix, "file.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "check.sh"), []byte("#!/bin/sh\ngrep -q fixed file.go\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return td
}

func writeArms(t *testing.T, path, armName, command string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"arms": []map[string]any{{
			"name":    armName,
			"command": []string{"sh", "-c", command},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func singleRunDir(t *testing.T, outDir string) string {
	t.Helper()
	ents, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected exactly one run dir under %s, got %d", outDir, len(ents))
	}
	return filepath.Join(outDir, ents[0].Name())
}

func loadResults(t *testing.T, path string) Results {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var res Results
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestRunEndToEnd drives a full run with a stub arm that emits a usage event
// on stdout (captured as events.jsonl) and edits the workspace, then checks
// pass/fail, token extraction, and the workspace delta.
func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "tasks")
	stubTask(t, tasksDir, "demo")
	arms := filepath.Join(dir, "arms.json")
	writeArms(t, arms, "stub", `printf '%s\n' '{"usage":{"input":10,"output":5}}'; printf 'fixed\n' >> file.go`)
	outDir := filepath.Join(dir, "out")

	if err := Main([]string{"run", "-tasks", tasksDir, "-arms", arms, "-out", outDir, "-timeout", "30s"}); err != nil {
		t.Fatal(err)
	}
	runDir := singleRunDir(t, outDir)
	res := loadResults(t, filepath.Join(runDir, "results.json"))
	if len(res.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(res.Runs))
	}
	r := res.Runs[0]
	if !r.Pass {
		t.Errorf("pass = false, want true (check-output: see art/check-output.log)")
	}
	if r.Usage.InputSum == nil || *r.Usage.InputSum != 10 || r.Usage.OutputSum == nil || *r.Usage.OutputSum != 5 {
		t.Errorf("tokens = %+v, want input 10 / output 5", r.Usage)
	}
	if r.FilesChanged < 1 || r.NonTestLOC < 1 {
		t.Errorf("delta = %+v, want at least one changed file with LOC", r)
	}
	if _, err := os.Stat(filepath.Join(runDir, "stub", "demo", "art", "events.jsonl")); err != nil {
		t.Error("raw events file not retained")
	}
}

// TestTimeoutKillsProcess pins the hard per-run deadline: a sleeping arm is
// killed, recorded as timed out, and never passes.
func TestTimeoutKillsProcess(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "tasks")
	stubTask(t, tasksDir, "demo")
	arms := filepath.Join(dir, "arms.json")
	writeArms(t, arms, "sleepy", "sleep 30")
	outDir := filepath.Join(dir, "out")

	start := time.Now()
	if err := Main([]string{"run", "-tasks", tasksDir, "-arms", arms, "-out", outDir, "-timeout", "200ms"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("run took %v — the sleeping arm was not killed on time", elapsed)
	}
	res := loadResults(t, filepath.Join(singleRunDir(t, outDir), "results.json"))
	r := res.Runs[0]
	if !r.TimedOut {
		t.Error("timed_out = false, want true")
	}
	if r.Pass {
		t.Error("pass = true for a timed-out run, want false")
	}
}

// TestRescoreNeverRunsArms pins the no-API-spend contract: rescore works
// from kept artifacts even when the arms file now names a command that
// would leave a marker if executed — and leaves no marker.
func TestRescoreNeverRunsArms(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "tasks")
	stubTask(t, tasksDir, "demo")
	arms := filepath.Join(dir, "arms.json")
	writeArms(t, arms, "stub", `printf '%s\n' '{"usage":{"input":10,"output":5}}'; printf 'fixed\n' >> file.go`)
	outDir := filepath.Join(dir, "out")
	if err := Main([]string{"run", "-tasks", tasksDir, "-arms", arms, "-out", outDir, "-timeout", "30s"}); err != nil {
		t.Fatal(err)
	}
	runDir := singleRunDir(t, outDir)

	// Swap in an arm that would betray its own execution with a marker file.
	marker := filepath.Join(dir, "canary")
	writeArms(t, arms, "trap", "touch "+marker)
	if err := Main([]string{"rescore", "-tasks", tasksDir, runDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("rescore executed an arm command — marker file exists")
	}
	res := loadResults(t, filepath.Join(runDir, "results-rescored.json"))
	r := res.Runs[0]
	if !r.Pass {
		t.Errorf("rescored pass = false, want true")
	}
	if r.Usage.InputSum == nil || *r.Usage.InputSum != 10 {
		t.Errorf("rescored tokens = %+v, want input 10", r.Usage)
	}
}

// TestWorkspaceDeltaSplit pins the over-engineering proxy: test code and
// non-test code land in separate buckets.
func TestWorkspaceDeltaSplit(t *testing.T) {
	fix := t.TempDir()
	ws := t.TempDir()
	write := func(base, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(base, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(fix, "a.go", "package a\n")
	write(ws, "a.go", "package a\n")          // unchanged
	write(ws, "normal.go", "package a\n\n")   // added, 2 lines
	write(ws, "x_test.go", "package a\n\n\n") // added, 3 lines (test bucket)

	d := workspaceDelta(fix, ws)
	if d == nil {
		t.Fatal("workspaceDelta returned nil")
	}
	if d.FilesChanged != 2 {
		t.Errorf("files_changed = %d, want 2", d.FilesChanged)
	}
	if d.NonTestLOC != 2 {
		t.Errorf("non_test_loc = %d, want 2", d.NonTestLOC)
	}
	if d.TestLOC != 3 {
		t.Errorf("test_loc = %d, want 3", d.TestLOC)
	}
}

// TestCopyDirPreservesExecBit pins that check.sh keeps its exec bit through
// the fixture copy (copyDir is used for workspaces; the same mode logic
// applies to any executable shipped in a fixture).
func TestCopyDirPreservesExecBit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	script := filepath.Join(src, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dst, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("exec bit lost: mode = %v, want 0755", st.Mode().Perm())
	}
}

func TestParseTaskMD(t *testing.T) {
	title, prompt := parseTaskMD("# Fix the bug\n\nBody line.", "fallback")
	if title != "Fix the bug" || prompt != "Body line." {
		t.Errorf("parseTaskMD = (%q, %q), want titled form", title, prompt)
	}
	title, prompt = parseTaskMD("Just a body.", "fallback")
	if title != "fallback" || prompt != "Just a body." {
		t.Errorf("parseTaskMD = (%q, %q), want fallback title", title, prompt)
	}
}
