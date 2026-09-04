//go:build !windows

package audit

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the entire
// group (including grandchildren holding the stdin/stdout pipes) can be
// killed on timeout.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the child's process group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
