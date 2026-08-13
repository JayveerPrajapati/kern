//go:build !windows

package optimize

import (
	"os/exec"
	"syscall"
)

// setProcessGroup runs the command in its own process group so the whole
// group can be killed when the context is cancelled.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup SIGKILLs the command's entire process group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
