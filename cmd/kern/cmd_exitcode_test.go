package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDoctorExitsNonzeroOnFail pins the e2e round-2 fix: `kern doctor`
// printed "verdict: failures" but exited 0. Any [fail]-level finding must
// map to exit 1 (warn-only runs stay 0).
func TestRunDoctorExitsNonzeroOnFail(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Force the binary-exec check to fail: setup.Bin() falls back to a
	// bare "kern-mcp" lookup that cannot resolve without PATH.
	t.Setenv("PATH", t.TempDir())
	root := jsonCliFixture(t)

	var code int
	out := captureStdout(t, func() {
		code = runDoctor([]string{root, "--json"})
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 with fail findings, got %d (out: %.200s)", code, out)
	}
	if !strings.Contains(out, `"Level": "fail"`) {
		t.Fatalf("expected a fail-level finding in JSON, got: %.400s", out)
	}
}

// TestRunDoctorExitCodeConsistency asserts the invariant both ways: the
// exit code is 1 exactly when a fail-level finding is present.
func TestRunDoctorExitCodeConsistency(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	var code int
	out := captureStdout(t, func() {
		code = runDoctor([]string{root, "--json"})
	})
	hasFail := strings.Contains(out, `"Level": "fail"`)
	if hasFail && code != 1 {
		t.Fatalf("fail findings present but exit code %d", code)
	}
	if !hasFail && code != 0 {
		t.Fatalf("no fail findings but exit code %d (out: %.300s)", code, out)
	}
}

// TestRunProjectMissingRootFails pins the e2e round-2 fix: `kern project
// map` rendered "Project: map (0 files)" with exit 0 for a nonexistent
// root. A missing root must fail loud (exit 1).
func TestRunProjectMissingRootFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected exitError panic for missing root")
		}
		e, ok := r.(exitError)
		if !ok {
			panic(r)
		}
		if e.code != 1 {
			t.Fatalf("expected exit code 1, got %d", e.code)
		}
	}()
	runProject([]string{missing})
}

// assertExitCode recovers the exitError sentinel and asserts its code —
// the shared contract check for the P2-8 straggler fixes.
func assertExitCode(t *testing.T, want int, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected exitError panic (code %d), got none", want)
		}
		e, ok := r.(exitError)
		if !ok {
			panic(r)
		}
		if e.code != want {
			t.Fatalf("exit code = %d, want %d", e.code, want)
		}
	}()
	fn()
}

// TestRunMissingRootFails (P2-8): kern brief/buddy/pack rendered empty output
// with exit 0 for a nonexistent root; a missing root must fail loud (exit 1).
func TestRunMissingRootFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	t.Run("brief", func(t *testing.T) { assertExitCode(t, 1, func() { runBrief([]string{missing}) }) })
	t.Run("buddy", func(t *testing.T) { assertExitCode(t, 1, func() { runBuddy([]string{missing}) }) })
	t.Run("pack", func(t *testing.T) { assertExitCode(t, 1, func() { runPack([]string{missing}) }) })
}

// TestParseSwallowBadFlagFails (P2-8): the mcp-tool mirror commands swallowed
// fs.Parse errors — an unknown flag printed usage to stderr but exited 0.
// The parse error must now fail loud (exit 2).
func TestParseSwallowBadFlagFails(t *testing.T) {
	bad := []string{"--nope"}
	cases := map[string]func([]string){
		"evidence-anchor":    runEvidenceAnchor,
		"stream":             runStream,
		"compose":            runCompose,
		"prompt-fill":        runPromptFill,
		"context-watch":      runContextWatch,
		"agent-fingerprint":  runAgentFingerprint,
		"memory-ranked":      runMemoryRanked,
		"agent-coordination": runAgentCoordination,
		"semantic-merge":     runSemanticMerge,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { assertExitCode(t, 2, func() { fn(bad) }) })
	}
	t.Run("register-host-sampler", func(t *testing.T) { assertExitCode(t, 2, func() { runRegisterHostSampler(bad) }) })
}
