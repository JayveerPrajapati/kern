//go:build windows

package audit

import "os/exec"

// setProcessGroup is a no-op on Windows.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the process directly on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
