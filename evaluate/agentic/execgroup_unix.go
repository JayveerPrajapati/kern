//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setGroupAttr puts the child in its own process group so a timeout can kill
// the whole tree (agent processes spawn children).
func setGroupAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the child's whole process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
