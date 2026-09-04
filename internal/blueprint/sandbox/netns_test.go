package sandbox

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestSandboxNetworkIsolationMacOSFallbackWarns verifies the graceful
// degradation path on macOS when sandbox-exec is absent (stripped installs):
// with NetworkIsolated and AllowUnisolated both set, a clear warning is
// printed to stderr and the command still runs — the unisolated override is
// visible, never silent. On macOS WITH sandbox-exec, isolation is available
// and this fallback cannot be exercised (skipped).
func TestSandboxNetworkIsolationMacOSFallbackWarns(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("macOS fallback behavior only (running on %s)", runtime.GOOS)
	}
	if networkIsolationAvailable() {
		t.Skip("sandbox-exec present: darwin network isolation is available; fallback not exercisable")
	}
	dir := g8Repo(t)

	// Capture os.Stderr so we can assert the warning is printed. Only
	// applyNetworkIsolation writes here — the sandboxed command's output is
	// captured via cmd.Stdout/Stderr pipes, so the pipe holds just the warning.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w

	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, Config{
		Timeout:         120 * time.Second,
		NetworkIsolated: true,
		AllowUnisolated: true,
	})

	w.Close()
	os.Stderr = oldStderr
	got, readErr := io.ReadAll(r)
	r.Close()
	if readErr != nil {
		t.Fatalf("read captured stderr: %v", readErr)
	}

	warn := string(got)
	if !strings.Contains(warn, "sandbox-exec not found") {
		t.Fatalf("expected a sandbox-exec-absent warning, got stderr: %q", warn)
	}
	if res.Error != "" {
		t.Fatalf("explicit override must not fail the sandbox: %s", res.Error)
	}
	if !res.Ok {
		t.Fatalf("expected the command to still run on macOS: exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}

// TestSandboxNetworkIsolationFailClosed verifies the fail-closed contract at
// the Run level: when isolation is requested on a platform that cannot
// provide it and no explicit override is given, Run refuses to execute and
// returns a clear error instead of silently running unisolated.
func TestSandboxNetworkIsolationFailClosed(t *testing.T) {
	if networkIsolationAvailable() {
		t.Skip("network isolation is available on this platform; fail-closed path only")
	}
	dir := g8Repo(t)

	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, Config{
		Timeout:         120 * time.Second,
		NetworkIsolated: true,
	})
	if res.Error == "" {
		t.Fatal("expected fail-closed error when isolation is requested but unavailable")
	}
	if !strings.Contains(res.Error, "use --allow-unisolated to override") {
		t.Errorf("error should point at the --allow-unisolated override, got: %q", res.Error)
	}
	if res.Ok {
		t.Error("expected Ok=false on fail-closed refusal")
	}
}

// TestSandboxNoIsolationByDefault verifies that network isolation is OFF by
// default: the Config flag defaults to false and a plain local command runs
// without any isolation interference. The Linux-specific SysProcAttr
// assertion (no CLONE_NEWNET) lives in netns_linux_test.go where the constant
// exists.
func TestSandboxNoIsolationByDefault(t *testing.T) {
	if DefaultConfig().NetworkIsolated {
		t.Fatal("expected NetworkIsolated to default to false")
	}

	dir := g8Repo(t)
	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
	if res.Error != "" {
		t.Fatalf("sandbox error: %s", res.Error)
	}
	if !res.Ok {
		t.Fatalf("expected un-isolated build to succeed: exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}

// isolationFinding returns the sandbox:network-isolation-unavailable finding
// if present in res, or nil.
func isolationFinding(t *testing.T, res domain.CheckResult) *domain.Finding {
	t.Helper()
	for i := range res.Findings {
		if res.Findings[i].RuleID == "sandbox:network-isolation-unavailable" {
			return &res.Findings[i]
		}
	}
	return nil
}

// TestSandboxCheckNetworkIsolationUnavailableFailClosed verifies the
// fail-closed contract at the Check level: when network isolation is
// requested on a platform where it's unavailable (non-Linux) and no override
// is given, the sandbox refuses to run and surfaces a clear error instead of
// silently downgrading a requested safety control. Nothing runs, so no
// isolation finding is emitted.
func TestSandboxCheckNetworkIsolationUnavailableFailClosed(t *testing.T) {
	if networkIsolationAvailable() {
		t.Skip("network isolation is available on this platform; fail-closed path only")
	}
	t.Setenv("BLUEPRINT_REQUIRE_NETISO", "")

	dir := g8Repo(t)
	check := NewDefaultCheck(WithNetworkIsolation())
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Status != domain.StatusError {
		t.Errorf("status = %s, want %s (fail-closed refusal)", res.Status, domain.StatusError)
	}
	if !strings.Contains(res.Error, "use --allow-unisolated to override") {
		t.Errorf("error should point at the --allow-unisolated override, got: %q", res.Error)
	}
	if f := isolationFinding(t, res); f != nil {
		t.Errorf("unexpected isolation finding on fail-closed refusal (nothing ran): %+v", f)
	}
}

// TestSandboxCheckNetworkIsolationOverrideRuns verifies that when network
// isolation is requested on an unsupported platform AND the caller explicitly
// overrides with WithAllowUnisolated, the check runs (build passes) and the
// artifact records a WARN finding — the unisolated run is visible, never
// silent.
func TestSandboxCheckNetworkIsolationOverrideRuns(t *testing.T) {
	if networkIsolationAvailable() {
		t.Skip("network isolation is available on this platform; unavailable-platform override only")
	}
	t.Setenv("BLUEPRINT_REQUIRE_NETISO", "")

	dir := g8Repo(t)
	check := NewDefaultCheck(WithNetworkIsolation(), WithAllowUnisolated())
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	f := isolationFinding(t, res)
	if f == nil {
		t.Fatalf("expected isolation-unavailable finding, got findings: %+v", res.Findings)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("severity = %s, want %s", f.Severity, domain.SeverityWarn)
	}
	if res.Status == domain.StatusError {
		t.Errorf("status = %s, want non-error (override run should complete)", res.Status)
	}
}

// TestSandboxCheckNetworkIsolationStrictBlocks verifies that
// BLUEPRINT_REQUIRE_NETISO=1 escalates the isolation-unavailable finding to
// BLOCK severity and the result to BLOCK status, even when the caller
// explicitly overrode with WithAllowUnisolated.
func TestSandboxCheckNetworkIsolationStrictBlocks(t *testing.T) {
	if networkIsolationAvailable() {
		t.Skip("network isolation is available on this platform; strict unavailable-platform block only")
	}
	t.Setenv("BLUEPRINT_REQUIRE_NETISO", "1")

	dir := g8Repo(t)
	check := NewDefaultCheck(WithNetworkIsolation(), WithAllowUnisolated())
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	f := isolationFinding(t, res)
	if f == nil {
		t.Fatalf("expected isolation-unavailable finding, got findings: %+v", res.Findings)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("severity = %s, want %s", f.Severity, domain.SeverityBlock)
	}
	if res.Status != domain.StatusBlock {
		t.Errorf("status = %s, want %s", res.Status, domain.StatusBlock)
	}
}

// TestSandboxCheckNoIsolationFindingWhenNotRequested verifies that when
// network isolation is NOT requested, no isolation-unavailable finding is
// emitted regardless of build outcome.
func TestSandboxCheckNoIsolationFindingWhenNotRequested(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck()
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if f := isolationFinding(t, res); f != nil {
		t.Errorf("unexpected isolation-unavailable finding when isolation not requested: %+v", f)
	}
}

// TestSandboxCheckNetworkIsolationAvailableNoFinding verifies that on Linux
// (where network isolation is available), requesting isolation does not
// produce the unavailable finding.
func TestSandboxCheckNetworkIsolationAvailableNoFinding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("network isolation availability is only exercised on linux")
	}

	dir := g8Repo(t)
	check := NewDefaultCheck(WithNetworkIsolation())
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if f := isolationFinding(t, res); f != nil {
		t.Errorf("unexpected isolation-unavailable finding on linux: %+v", f)
	}
}
