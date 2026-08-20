//go:build !windows

// Package processgroup provides a cross-platform way to run a command in its
// own process group so the entire group (the command and any grandchildren it
// spawns) can be killed together on timeout. Without this, killing only the
// direct child leaves orphaned processes running — a classic unbounded
// background-process escape that also defeats exec timeouts.
package processgroup

import (
	"os/exec"
	"syscall"
)

// Set runs cmd in its own process group so the whole group can later be
// killed together. It is a no-op where process groups are unavailable.
func Set(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

// Kill SIGKILLs cmd's entire process group (the group leader is cmd itself
// because of Set, so -pid addresses every descendant). It is safe to call
// after the direct child has exited: lingering grandchildren are killed, and
// a pid that has been recycled would have a fresh pgid that Set's leader
// owned, so -pid targets our own group.
func Kill(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
