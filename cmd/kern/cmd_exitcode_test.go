package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/verification"
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
	if !strings.Contains(out, `"level": "fail"`) {
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
	hasFail := strings.Contains(out, `"level": "fail"`)
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
	t.Run("buddy", func(t *testing.T) { assertExitCode(t, 1, func() { runBrief([]string{missing}) }) })
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

// TestVerifyExitCodeContract pins the F7/F19 exit mapping: FAIL is the only
// hard failure (1); WARN, SKIPPED, PASS and PASS_WITH_WARNING are reported
// outcomes and exit 0.
func TestVerifyExitCodeContract(t *testing.T) {
	cases := []struct {
		verdict verification.Verdict
		want    int
	}{
		{verification.VerdictPass, 0},
		{verification.VerdictPassWithWarning, 0},
		{verification.VerdictWarn, 0},
		{verification.VerdictSkipped, 0},
		{verification.VerdictFail, 1},
		{verification.VerdictBlocked, 0},
		{verification.VerdictNotRun, 0},
	}
	for _, c := range cases {
		if got := verifyExitCode(c.verdict); got != c.want {
			t.Errorf("verifyExitCode(%s) = %d, want %d", c.verdict, got, c.want)
		}
	}
}

// TestVerifyOutcomeLineWording pins F7: "verification FAILED" is reserved for
// a FAIL verdict; WARN and SKIPPED have their own wording, and a SKIPPED
// verdict names the reason (e.g. "govulncheck not installed").
func TestVerifyOutcomeLineWording(t *testing.T) {
	skipped := verification.VerificationResult{
		Verdict: verification.VerdictSkipped,
		CVE: &verification.CVEResult{
			Status: verification.StatusSkipped,
			Detail: "govulncheck not installed; install with: go run golang.org/x/vuln/cmd/govulncheck@latest",
		},
	}
	line := verifyOutcomeLine(skipped)
	if !strings.Contains(line, "verification SKIPPED") {
		t.Errorf("SKIPPED outcome line must say SKIPPED, got: %s", line)
	}
	if !strings.Contains(line, "govulncheck not installed") {
		t.Errorf("SKIPPED outcome line must name the reason, got: %s", line)
	}
	if strings.Contains(line, "verification FAILED") {
		t.Errorf("SKIPPED outcome line must not say FAILED, got: %s", line)
	}
	if got := verifyOutcomeLine(verification.VerificationResult{Verdict: verification.VerdictFail}); !strings.Contains(got, "verification FAILED") {
		t.Errorf("FAIL outcome line must say FAILED, got: %s", got)
	}
	if got := verifyOutcomeLine(verification.VerificationResult{Verdict: verification.VerdictWarn}); !strings.Contains(got, "verification WARNED") {
		t.Errorf("WARN outcome line must say WARNED, got: %s", got)
	}
	if got := verifyOutcomeLine(verification.VerificationResult{Verdict: verification.VerdictSkipped}); !strings.Contains(got, "verification SKIPPED") {
		t.Errorf("bare SKIPPED outcome line must still say SKIPPED, got: %s", got)
	}
}

// TestReviewRiskExitsPolicy pins F19: kern review / kern changes with risk
// found exit 3 (policy family) on both the text and --json paths, while a
// review of an unindexed file (no risk) exits 0.
func TestReviewRiskExitsPolicy(t *testing.T) {
	root := jsonCliFixture(t)
	if _, err := index.Build(root); err != nil {
		t.Fatalf("index build: %v", err)
	}
	// main.go is indexed and its symbols (main, helper) are untested, so
	// TotalRisk > 0 → policy exit 3 (F19).
	assertExitCode(t, 3, func() { runChanges("review", []string{"--root", root, "--file", "main.go"}) })
	assertExitCode(t, 3, func() { runChanges("review", []string{"--root", root, "--json", "--file", "main.go"}) })
	assertExitCode(t, 3, func() { runChanges("changes", []string{"--root", root, "--file", "main.go"}) })
	assertExitCode(t, 3, func() { runChanges("changes", []string{"--root", root, "--json", "--file", "main.go"}) })
	// A file that is not in the index contributes no risk → exit 0 (no
	// exitError panic).
	runChanges("review", []string{"--root", root, "--file", "not_indexed.go"})
}

// TestRunMutationMinScoreGate (F-MU1): findings-producing commands exit
// non-zero — a completed mutation run with surviving mutants below
// --min-score must return 1 (CI gate), while the same run without the flag
// exits 0 (advisory). The fixture is deliberately untestable: one trivial
// function, no test file, so the score is 0%.
func TestRunMutationMinScoreGate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module muttest\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte("package muttest\n\nfunc Twice(n int) int {\n\treturn n + n\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Advisory run: survivors exist but no gate is set.
	if code := runMutationTest([]string{dir}); code != 0 {
		t.Fatalf("advisory run must exit 0, got %d", code)
	}
	// Gated run: score 0 < 50 must exit 1.
	if code := runMutationTest([]string{dir, "--min-score", "50"}); code != 1 {
		t.Fatalf("gated run must exit 1, got %d", code)
	}
	// Gate satisfied: 0-score vs --min-score 0 must exit 0.
	if code := runMutationTest([]string{dir, "--min-score", "0"}); code != 0 {
		t.Fatalf("satisfied gate must exit 0, got %d", code)
	}
}
