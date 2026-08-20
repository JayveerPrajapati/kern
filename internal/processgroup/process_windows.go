//go:build windows

// Package processgroup provides a cross-platform way to run a command in its
// own process group so the whole group can be killed together on timeout.
package processgroup

import "os/exec"

// Set is a no-op on Windows: there is no syscall process group, so context
// cancellation still kills only the direct child (matching prior behaviour).
func Set(cmd *exec.Cmd) {}

// Kill is a no-op on Windows for the same reason as Set.
func Kill(cmd *exec.Cmd) {}
