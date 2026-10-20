//go:build windows

package setup

import "os/exec"

func detachCmd(cmd *exec.Cmd) {}
