//go:build windows

package daemon

import (
	"net"
	"os/exec"
)

// Supported is false on Windows: the daemon speaks unix domain sockets, and
// the CLI's one-process-per-command path already works there.
func Supported() bool { return false }

func listenUnix(string) (net.Listener, error) {
	return nil, ErrUnsupported
}

func detach(*exec.Cmd) {}
