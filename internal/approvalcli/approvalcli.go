// Package approvalcli implements the approval/governance command family of
// the kern CLI: `kern approve`, `kern reject`, `kern approval`, `kern audit`
// and `kern policy`. It was extracted from cmd/kern (Phase 3 of the CLI
// shrink) following the earlier CLI-family extraction precedent: the package
// owns the family's flag parsing, rendering and internal helpers, and exports
// Run* entrypoints that return the process exit code (int) to the cmd/kern
// dispatch table, which wires them via the commandTable like the other
// extracted families.
//
// Exit-code contract (pinned by cmd/kern tests, must survive EXACTLY):
//
//	usage errors  -> 2 (no stack trace)
//	findings / runtime errors -> 1
//	denials / decided-state policy outcomes -> 3
//
// The command bodies keep the house fatal style (panic with the exitError
// sentinel); each Run* entrypoint recovers the sentinel and converts it back
// into the exit code, so cmd/kern's main() never sees a foreign panic and
// the dispatch table always receives a plain int.
package approvalcli

import (
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/cliout"
)

// exitError is the sentinel panic type used by fatal, fatalUsage and
// fatalPolicy — the cmd/kern helpers.go convention, preserved verbatim. It
// is recovered inside this package by run (each exported entrypoint), which
// converts it back into the process exit code.
type exitError struct{ code int }

// fatal prints an error to stderr and signals exit code 1 (the Unix
// convention for runtime errors).
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and signals exit code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown subcommands. It must never print a stack trace.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// fatalPolicy prints an error to stderr and signals exit code 3 (the
// decided-state / policy-outcome convention — see `kern exitcode`). Use it
// for approvals that are already decided and similar policy outcomes.
func fatalPolicy(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 3})
}

// run executes a family command body, recovering the exitError sentinel and
// returning its code — the shared wrapper behind every exported entrypoint.
// Command bodies that return an int (runApproval's sub-dispatch) propagate
// it; void bodies return 0. Non-sentinel panics are re-panicked (a genuine
// bug must not be swallowed into an exit code).
func run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r) // re-panic non-sentinel
		}
	}()
	return fn()
}

// RunApprove executes `kern approve [id] [--approver X] [--reason R]
// [--reject] [--root R]` — resolve an approval gate.
func RunApprove(rest []string) int {
	return run(func() int { runApprove(rest); return 0 })
}

// RunReject executes `kern reject <id> [--reason R] [--approver X]
// [--root R]` — reject a pending approval request.
func RunReject(rest []string) int {
	return run(func() int { runReject(rest); return 0 })
}

// RunAudit executes `kern audit [task-id] [--json] [--root R]`, including
// the `audit append` / `audit repair` subcommands.
func RunAudit(rest []string) int {
	return run(func() int { runAudit(rest); return 0 })
}

// RunPolicy executes `kern policy <set|get|apply> [flags]`.
func RunPolicy(rest []string) int {
	return run(func() int { runPolicy(rest); return 0 })
}

// RunApproval executes `kern approval <subcommand> [flags]` — the
// approval-family surface beyond the decision commands (currently `wait`).
func RunApproval(rest []string) int {
	return run(func() int { return runApproval(rest) })
}

// printJSON renders v as indented JSON on stdout (the family's --json
// outputs: audit rows, policy documents). Same contract as the cmd/kern
// helper it replaces: a marshal failure is a fatal error.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// isDir reports whether p exists and is a directory.
func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// projectRoot returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble the family's commands repeat.
func projectRoot(f flags) string {
	if f.root != "" {
		return f.root
	}
	return "."
}

// parseFlagsOrDie parses subcommand flags, signalling a usage error (exit
// code 2) on failure — the flag-parsing preamble every family command
// repeats.
func parseFlagsOrDie(rest []string) (flags, []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}
