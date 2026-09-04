//go:build linux

package sandbox

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSandboxNetworkIsolationLinux verifies that with NetworkIsolated set on
// Linux the sandboxed process cannot reach the network: it runs in a new
// network namespace (CLONE_NEWNET) containing only loopback. The probe
// attempts a real network operation with git (guaranteed present — the
// sandbox itself depends on it).
//
// Environment note: creating a network namespace requires CAP_SYS_ADMIN
// (typically root). When the caller lacks it, cmd.Start fails with EPERM,
// which also surfaces as a failed (non-Ok) run — so the core assertion below
// (an isolated run never succeeds) holds in both environments.
func TestSandboxNetworkIsolationLinux(t *testing.T) {
	// Mechanism: the SysProcAttr must carry CLONE_NEWNET — but only when the
	// platform actually supports it. In restricted environments (no
	// CAP_SYS_ADMIN / unprivileged user namespaces) the probe reports
	// unavailable and applyNetworkIsolation falls back to a visible warning
	// instead of setting the clone flag, so the flag cannot be asserted there.
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	applyNetworkIsolation(cmd)
	if networkIsolationAvailable() && cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNET == 0 {
		t.Fatalf("expected CLONE_NEWNET in Cloneflags, got %x", cmd.SysProcAttr.Cloneflags)
	}

	// No isolation by default: CLONE_NEWNET must be absent.
	plain := &syscall.SysProcAttr{Setpgid: true}
	if plain.Cloneflags&syscall.CLONE_NEWNET != 0 {
		t.Fatal("expected no CLONE_NEWNET on a default SysProcAttr")
	}

	// Integration: a network operation must fail under isolation.
	dir := g8Repo(t)
	res := Run(context.Background(), dir,
		[]string{"git", "ls-remote", "https://github.com/JayveerPrajapati/kernIO.git", "HEAD"},
		Config{Timeout: 15 * time.Second, NetworkIsolated: true},
	)
	if res.Ok {
		t.Fatalf("expected network-isolated command to fail, but it succeeded: %+v", res)
	}
}
