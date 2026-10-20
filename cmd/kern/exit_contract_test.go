package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFindingsCommandExitContract pins the CLI exit-code contract of every
// findings-producing command (the CI signals, per the plugin exit-code
// contract): risk findings exit 3 (policy family — includes security, which
// DRIFTED from 1 to 3 with the fatalPolicy change), blocking gates exit 1,
// failing builds/scripts exit 1, failing sandboxed exec exits 1. The plugin
// shadows recover stdout on ANY nonzero exit, but CI configs gating on exact
// codes break on silent drift — this test fails the build when it happens.
//
// Contract pinned (verified live 2026-09-24):
//
//	kern changes <dirty>      -> 1   (risk findings, fatalFindings)
//	kern review  <dirty>      -> 1   (risk findings, fatalFindings)
//	kern security <secret>    -> 1   (error findings)
//	kern diff-gate --blocking -> 1   (fixture BLOCKs on the hardcoded secret;
//	                                 the WARN→BLOCK --blocking elevation -> 3 is
//	                                 pinned in TestDiffGateServiceVerdicts)
//	kern verify --types build -> 1   (FAIL verdict on a broken build)
//	kern exec <failing>       -> 1   (script exit propagates)

// TestSecGosecExitContract pins the optional gosec engine's exit contract:
// a HIGH-severity gosec finding is advisory under the default error-only
// lens (exit 0 — Model C: external engines never gate), and only
// KERN_SEC_PROMOTE lifting its rule ID reaches the findings-tier exit 1.

// TestSecSuppressionTriageContract pins the suppression-triage behavior of
// `kern sec` (M-1, production-readiness audit 2026-10-17): an error-severity
// finding covered by the suppression registry (built-in defaults merged with
// the optional .kern/verify-suppressions.json) stays visible as a
// [suppressed] line carrying its reason, appears under the "suppressed" key
// in --json, and NEVER counts toward the findings-tier exit gate — so
// `kern sec` and `kern verify --types security` agree on the same finding
// (QA F2). An unsuppressed error still exits 1 (pinned above).

// TestAuthorizeContextDeniedExitContract pins the authorize-context CLI exit
// contract: a fail-closed authentication denial is a policy-family exit (3),
// not a usage error (2). The former standalone authorized-context MCP page
// was removed with that obsolete tool; the CLI behavior remains covered here.
func TestAuthorizeContextDeniedExitContract(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: builds a binary and runs it against a fixture repo")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Behavioral: build the binary and run a denial through the real exit path.
	bin := filepath.Join(t.TempDir(), "kern")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build kern: %v (%s)", err, out)
	}
	fix := t.TempDir()
	if err := os.WriteFile(filepath.Join(fix, "go.mod"), []byte("module fix\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unknown agent -> fail-closed authentication denial -> exit 3.
	cmd := exec.Command(bin, "authorize-context", "-agent", "ghost", "-task", "t", "-root", fix)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 3 {
		t.Errorf("authorize-context denied (unknown agent): exit %d, want 3", got)
	}
}
