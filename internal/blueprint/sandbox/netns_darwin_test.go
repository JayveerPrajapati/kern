//go:build darwin

package sandbox

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSandboxNetworkIsolationDarwin verifies that with NetworkIsolated set on
// macOS the sandboxed process cannot reach the network: the command is
// rewritten to run under sandbox-exec with a network-deny profile. The probe
// attempts a real network operation with git (guaranteed present — the
// sandbox itself depends on it).
func TestSandboxNetworkIsolationDarwin(t *testing.T) {
	if !networkIsolationAvailable() {
		t.Skip("sandbox-exec not present; darwin network isolation unavailable")
	}

	// Mechanism: the cmd must be rewritten to run under sandbox-exec.
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	applyNetworkIsolation(cmd)
	if cmd.Path != sandboxExecPath {
		t.Fatalf("expected cmd.Path to be %s, got %q", sandboxExecPath, cmd.Path)
	}
	if len(cmd.Args) < 5 || cmd.Args[0] != "sandbox-exec" {
		t.Fatalf("expected sandbox-exec wrapper at the front of Args, got %v", cmd.Args)
	}
	// The wrapper must carry -p <profile> -- before the original command.
	if cmd.Args[1] != "-p" || cmd.Args[3] != "--" {
		t.Fatalf("expected [-p <profile> -- ...] wrapper shape, got %v", cmd.Args)
	}
	if cmd.Args[2] != networkDenyProfile {
		t.Fatalf("expected network-deny profile in Args, got %q", cmd.Args[2])
	}
	if last := cmd.Args[len(cmd.Args)-1]; last != "true" {
		t.Fatalf("expected original command preserved at the end, got %v", cmd.Args)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected SysProcAttr.Setpgid preserved through the wrapper")
	}

	// Availability: with sandbox-exec present, isolation is available.
	if !NetworkIsolationAvailable() {
		t.Fatal("expected NetworkIsolationAvailable() to be true when sandbox-exec is present")
	}

	// Integration: a network operation must fail under isolation. The sandbox
	// itself must run cleanly (no sandbox Error) — only the network access is
	// denied, surfacing as a prompt non-zero exit from git.
	dir := g8Repo(t)
	res := Run(context.Background(), dir,
		[]string{"git", "ls-remote", "https://github.com/JayveerPrajapati/kernIO.git", "HEAD"},
		Config{Timeout: 15 * time.Second, NetworkIsolated: true},
	)
	if res.Error != "" {
		t.Fatalf("expected sandbox to run (network denied, not sandbox failure): %s", res.Error)
	}
	if res.Ok {
		t.Fatalf("expected network-isolated command to fail, but it succeeded: %+v", res)
	}
	if res.TimedOut {
		t.Fatalf("expected the network probe to be denied promptly, not time out: %+v", res)
	}
}
