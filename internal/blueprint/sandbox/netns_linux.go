//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// applyNetworkIsolation starts the sandboxed process in a new network
// namespace (CLONE_NEWNET). The namespace starts with only the loopback
// interface, so the command has no external network egress.
//
// Requires CAP_SYS_ADMIN in the caller's user namespace (typically root). If
// the caller lacks the capability, cmd.Start fails with EPERM, which surfaces
// as a sandbox error rather than running the command unisolated. When
// CLONE_NEWNET is unavailable (probe failed), the caller must have explicitly
// opted into an unisolated run (AllowUnisolated) to reach this point — we
// print a clear warning and continue WITHOUT isolation instead of setting the
// clone flag, so the override path works. The warning keeps the override
// visible, never a silent downgrade.
func applyNetworkIsolation(cmd *exec.Cmd) {
	if !networkIsolationAvailable() {
		// Run() fail-closes before this when isolation was requested without
		// AllowUnisolated, so reaching here means the caller explicitly opted
		// into an unisolated run. Warn — never a silent downgrade.
		fmt.Fprintf(os.Stderr, "blueprint: warning: CLONE_NEWNET network isolation is not available here (requires CAP_SYS_ADMIN or unprivileged user namespaces); continuing WITHOUT network isolation\n")
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
}

// netnsProbeOnce guards the runtime probe so it runs at most once per
// process. The result cannot change during a process lifetime (capabilities
// and namespace restrictions are set at login/container start).
var (
	netnsProbeOnce sync.Once
	netnsAvailable bool
)

// probeNetworkIsolation tries to start a trivial process in a new network
// namespace. If the start succeeds, CLONE_NEWNET is available (the caller has
// CAP_SYS_ADMIN or unprivileged user namespaces are enabled). If it fails
// (EPERM / EACCES), isolation is unavailable — which is the case on
// restricted CI runners (e.g. GitHub Actions ubuntu runners that disable
// unprivileged user namespaces and run non-root).
func probeNetworkIsolation() bool {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	cmd.Wait()
	return true
}

// networkIsolationAvailable reports whether this platform supports network
// namespace isolation at runtime. Linux supports CLONE_NEWNET at the API
// level, but the operation requires CAP_SYS_ADMIN (or an unprivileged user
// namespace); restricted environments such as GitHub Actions runners deny it.
// The probe forks a trivial process with CLONE_NEWNET once and caches the
// result. An actual runtime failure during a sandboxed run (e.g. a race with
// a capability change) already surfaces as a sandbox error.
func networkIsolationAvailable() bool {
	netnsProbeOnce.Do(func() {
		netnsAvailable = probeNetworkIsolation()
	})
	return netnsAvailable
}

// NetworkIsolationAvailable reports whether this platform supports network
// namespace isolation. Exported so callers (CI, CLI) can decide whether to
// request isolation at all — requesting it on an unsupported platform emits a
// WARN finding (see Check.Run), which would add noise to otherwise-clean
// verdicts. Callers that want the finding signal should still request it
// explicitly; callers that want silent best-effort should gate on this.
func NetworkIsolationAvailable() bool { return networkIsolationAvailable() }
