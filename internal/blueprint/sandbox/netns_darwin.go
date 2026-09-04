//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// networkDenyProfile is the sandbox-exec profile applied when network
// isolation is requested on macOS. It denies ALL network operations — bind,
// inbound, outbound — including loopback and unix-domain sockets. This is
// stricter than the Linux behavior (a fresh netns still has loopback) and
// matches the P0.3 definition of done: a localhost bind/connect is denied
// under isolation.
//
// The profile must lead with (allow default): a non-empty sandbox-exec
// profile is deny-by-default, so without it the wrapped command cannot even
// exec. The trailing (deny network*) overrides the default allow for the
// network domain only (last matching rule wins), leaving file/process/etc.
// access untouched. Verified on macOS 26: exec and local file operations work
// normally while external and localhost connections are denied.
const networkDenyProfile = `(version 1)
(allow default)
(deny network*)`

// sandboxExecPath is the standard location of sandbox-exec on macOS. It
// ships with every standard install but may be absent on stripped-down
// systems.
const sandboxExecPath = "/usr/bin/sandbox-exec"

var (
	sandboxExecOnce sync.Once
	sandboxExecOK   bool
)

// sandboxExecPresent reports whether sandbox-exec exists on this machine.
// The result is cached — the filesystem cannot change mid-process.
func sandboxExecPresent() bool {
	sandboxExecOnce.Do(func() {
		_, err := os.Stat(sandboxExecPath)
		sandboxExecOK = err == nil
	})
	return sandboxExecOK
}

// applyNetworkIsolation rewrites the command so it runs under
// /usr/bin/sandbox-exec with a network-deny profile: the wrapper becomes the
// actual process and the original command runs as its child inside the
// sandbox. Because Run() sets SysProcAttr.Setpgid before calling this, the
// wrapper is the process-group leader and killProcessGroup still reaps the
// whole tree (wrapper + child) on timeout/cancel.
//
// cmd.Dir, cmd.Env, cmd.Stdout/Stderr and cmd.SysProcAttr are preserved
// untouched — sandbox-exec inherits them and passes them to the child.
func applyNetworkIsolation(cmd *exec.Cmd) {
	if !sandboxExecPresent() {
		// Run() fail-closes before this when isolation was requested without
		// AllowUnisolated, so reaching here means the caller explicitly opted
		// into an unisolated run. Warn — never a silent downgrade.
		fmt.Fprintf(os.Stderr, "blueprint: warning: sandbox-exec not found; network isolation is not supported on darwin; continuing WITHOUT network isolation\n")
		return
	}
	// Wrap: sandbox-exec -p <profile> -- <original args...>
	cmd.Args = append([]string{"sandbox-exec", "-p", networkDenyProfile, "--"}, cmd.Args...)
	cmd.Path = sandboxExecPath
}

// networkIsolationAvailable reports whether this platform supports network
// isolation. On macOS this requires /usr/bin/sandbox-exec.
func networkIsolationAvailable() bool { return sandboxExecPresent() }

// NetworkIsolationAvailable reports whether this platform supports network
// isolation. Exported so callers (CI, CLI) can decide whether to request
// isolation at all — requesting it on an unsupported platform emits a WARN
// finding (see Check.Run), which would add noise to otherwise-clean verdicts.
// Callers that want the finding signal should still request it explicitly;
// callers that want silent best-effort should gate on this.
func NetworkIsolationAvailable() bool { return networkIsolationAvailable() }
