//go:build !linux && !darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// applyNetworkIsolation cannot create a network namespace on this platform
// with the Go stdlib (Windows and other non-Linux, non-darwin platforms have
// no CLONE_NEWNET equivalent, and macOS is handled by netns_darwin.go via
// sandbox-exec), so it prints a clear warning to stderr and continues WITHOUT
// isolation. This path is only reachable when the caller explicitly opted
// into an unisolated run (Config.AllowUnisolated): Run() fails closed before
// this when isolation was requested without that override. The warning keeps
// the override visible — never a silent downgrade. True network isolation
// here would require platform-specific mechanisms (network extensions) that
// are out of scope.
func applyNetworkIsolation(cmd *exec.Cmd) {
	fmt.Fprintf(os.Stderr, "blueprint: warning: --isolate-network is not supported on %s; continuing WITHOUT network isolation\n", runtime.GOOS)
}

// networkIsolationAvailable reports whether this platform supports network
// namespace isolation. Non-Linux, non-darwin platforms have no stdlib
// CLONE_NEWNET equivalent, so isolation is unavailable.
func networkIsolationAvailable() bool { return false }

// NetworkIsolationAvailable reports whether this platform supports network
// namespace isolation. Exported so callers (CI, CLI) can decide whether to
// request isolation at all. See netns_linux.go for the full rationale.
func NetworkIsolationAvailable() bool { return networkIsolationAvailable() }
