// Package contextcli implements the CLI command family for context, locks
// and the architecture guard (workstream 3.6-A — thin cmd/kern): the
// `kern context`, `kern lock`/`unlock`/`status`, `kern guard` and
// `kern authorize-context` entry points. Flag parsing, usage guards and
// rendering live here; the tool bodies are the internal/context, internal/
// lock, internal/guard and internal/governance packages.
//
// The family landed as a TOP-LEVEL sibling rather than inside the
// internal/mcp/<leaf> packages because the internal/mcp monolith subtree is
// ratcheted at a 19,000 LOC cap — the ARCHITECTURE.md contract prescribes
// exactly this extraction shape for anything that would grow the subtree
// (the mcp→mcpserve and mcp→metaroute pattern): mirrors landing inside a
// leaf cannot relieve the subtree, mirrors outside it do not count against
// it. This package follows the internal/mcpcli precedent from tranche 5
// (commit 862801c): entry points return the process exit code as an int, a
// local exitError sentinel is recovered inside, and each command owns a
// per-command parse*Flags built on the shared cliflags.Parser.
package contextcli

import (
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/index"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// exitError is the sentinel panic type used by fatal and fatalUsage — the
// cmd/kern helpers.go convention, preserved verbatim. It is recovered inside
// this package by run, which converts it back into the process exit code.
type exitError struct{ code int }

// fatal prints an error to stderr and exits with code 1 (the Unix convention
// for runtime errors). Keep fatalUsage() for usage errors. Like fatalUsage,
// it panics with the exitError sentinel instead of calling os.Exit directly
// so the caller can recover the exit code.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and exits with code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown commands. Keep fatal() for runtime errors.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// fatalPolicy prints an error to stderr and exits with code 3 (the
// decided-state / policy-outcome convention — see `kern exitcode`). Use it
// for approvals that are already decided and similar policy outcomes.
func fatalPolicy(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 3})
}

// fatalFindings prints an error to stderr and exits with code 1 (findings
// tier — see `kern exitcode`). Use it for findings-family outcomes: reported
// results that are signals for scripts and CI, not denials.
func fatalFindings(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// run executes fn and recovers the exitError sentinel, converting it back
// into the process exit code (the mcpcli pattern). A non-sentinel panic is
// re-raised: only the family's own fatal helpers may control the exit code.
func run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	return fn()
}

// version is the build-stamped release version reported inside `kern guard
// --sarif` output. It mirrors the cmd/kern main.go pattern: a literal "dev"
// initializer keeps the legacy `-ldflags "-X main.version=..."` rewrite
// working for the CLI binary, and init() adopts the shared
// internal/version.Version (either "dev" or the newer
// "-X .../internal/version.Version=..." stamp) when unstamped.
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

// printJSON renders v as indented JSON on stdout, clipping oversized string
// fields first (the 512 KiB safety guard). The clip machinery lives in
// internal/cliout — the shared home for every --json surface's output path;
// a marshal failure stays a fatal error with the historical "printJSON: %v"
// phrasing.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// loadOrBuild delegates to index.LoadOrBuild — the canonical shared
// implementation. Kept as a thin wrapper for the family's callers.
func loadOrBuild(root string) (*index.Index, error) {
	return index.LoadOrBuild(root)
}

// rootOf returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble every subcommand repeats
// (previously the shared cmd/kern projectRoot helper).
func rootOf(root string) string {
	if root != "" {
		return root
	}
	return "."
}
