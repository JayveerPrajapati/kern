//go:build !windows

package daemon

import (
	"net"
	"os/exec"
	"syscall"
)

// Supported reports whether this platform can host the daemon.
func Supported() bool { return true }

func listenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

// detach survives the parent process: the daemon outlives the CLI call that
// started it and never receives the caller's terminal signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
