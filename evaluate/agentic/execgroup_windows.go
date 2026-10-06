//go:build !unix

package main

import "os/exec"

// setGroupAttr is a no-op where process groups do not exist; the direct
// child is still killed on timeout.
func setGroupAttr(*exec.Cmd) {}

// killGroup kills just the direct child.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
