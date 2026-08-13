//go:build windows

package optimize

import "os/exec"

// Windows has no syscall process groups; context cancel kills the direct
// child (cmd.exe), matching the previous behaviour.
func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {}
