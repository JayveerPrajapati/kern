//go:build windows

package llm

import "os/exec"

// Windows has no POSIX process groups. setProcessGroup is a no-op; on
// timeout/cancel we kill the direct child (cmd.exe), matching the previous
// behaviour. (Context cancellation also kills the direct child via
// exec.CommandContext.)
func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
