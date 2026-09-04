package main

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/cockpit"
)

// runOps executes the KernOps governed autonomous engineering cockpit.
func runOps(args []string) int {
	return cockpit.RunOpsCLI(args, os.Stdout, os.Stderr)
}
