package main

// cmd_exec.go is the thin CLI shell for the exec/sandbox/validate/udiff/heal
// family (workstream 3.6-A — family extraction): the business logic lives in
// the internal landing packages (internal/execution, internal/sandbox,
// internal/heal), and the dispatch table delegates to those entry points.
// Each run* returns the process exit code (int) straight from the landing
// package; cmd/kern adds flags + delegation only.

import (
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
)

func runBuild(rest []string) int {
	return execution.RunBuild(rest)
}

func runValidate(rest []string) int {
	return execution.RunValidate(rest)
}

func runHeal(rest []string) int {
	return heal.RunHeal(rest)
}

func runUdiff(rest []string) int {
	return execution.RunUdiff(rest)
}

func runSandbox(rest []string) int {
	return sandbox.RunSandbox(rest)
}

func runExec(rest []string) int {
	return execution.RunExec(rest)
}
