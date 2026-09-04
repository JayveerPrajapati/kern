//go:build windows

package sandbox

import "os/exec"

// setProcessGroup is a no-op on Windows; process groups use CREATE_PROCESS_GROUP
// via CREATE_NEW_PROCESS_GROUP flag but Go's exec doesn't expose it directly.
// On Windows, killing the process via cmd.Process.Kill() is sufficient since
// child processes spawned by the sandbox are typically direct children.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the process directly on Windows (no process-group kill).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
