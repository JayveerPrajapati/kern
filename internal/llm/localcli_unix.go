//go:build !windows

package llm

import (
	"os/exec"
	"syscall"
)

// setProcessGroup runs the command in its own process group so the whole
// group — the agent CLI and its children (e.g. `opencode run`'s spawned
// tools) — can be killed when the context is cancelled or the per-call
// timeout fires. Killing only the direct child previously orphaned
// grandchildren, which kept running (CPU-burning, editing files) under
// PPID 1.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup SIGKILLs the command's entire process group. The direct
// child is the group leader (Setpgid), so -pid addresses the whole group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
