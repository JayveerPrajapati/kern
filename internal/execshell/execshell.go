// Package execshell builds the platform-native shell invocation for a command
// string. Unix uses `sh -c`; Windows uses `cmd /c` (P0-6, 2026-10-28
// adversarial audit: three exec surfaces were hard-wired to sh, so
// `kern verify --command`, host sampling and ShellDeployer failed on a default
// Windows host). Keeping the selection in one place makes the surfaces
// impossible to drift.
package execshell

import (
	"context"
	"os/exec"
	"runtime"
)

// ShellArgs returns the shell program and arguments for one command string on
// the given GOOS. goos is explicit so tests can pin both branches on any host.
func ShellArgs(goos, command string) (string, []string) {
	if goos == "windows" {
		return "cmd", []string{"/c", command}
	}
	return "sh", []string{"-c", command}
}

// CommandContext returns an *exec.Cmd that runs command through the native
// shell of the current platform.
func CommandContext(ctx context.Context, command string) *exec.Cmd {
	name, args := ShellArgs(runtime.GOOS, command)
	return exec.CommandContext(ctx, name, args...)
}
