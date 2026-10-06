package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/verifycmd"
)

// verifyCommandFlags are the `kern verify` flags that select command mode.
var verifyCommandFlags = map[string]string{
	"--command": "command",
	"--output":  "output",
	"--anchor":  "anchor",
	"--root":    "root",
	"--timeout": "timeout",
}

// runVerifyCommand handles `kern verify --command "<cmd>" [--output MODE]`
// and `kern verify --anchor ID --output lines:A-B`. It reports whether the
// arguments were command mode; every other `kern verify` form falls through
// to the normal verification path untouched. The exit code is the command's
// own, so scripts can rely on it.
func runVerifyCommand(rest []string) bool {
	args := map[string]any{}
	for i := 0; i < len(rest); i++ {
		name, val, hasEq := strings.Cut(rest[i], "=")
		key, ok := verifyCommandFlags[name]
		if !ok {
			continue
		}
		if !hasEq {
			if i+1 >= len(rest) {
				fatalUsage("verify: %s needs a value", name)
			}
			i++
			val = rest[i]
		}
		args[key] = val
	}
	if _, ok := args["command"]; !ok {
		if _, ok := args["anchor"]; !ok {
			return false
		}
	}
	out, exit, err := verifycmd.VerifyCommand(context.Background(), args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	fmt.Println(out)
	if exit != 0 {
		os.Exit(1)
	}
	return true
}
