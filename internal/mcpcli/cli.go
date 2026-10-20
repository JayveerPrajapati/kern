// Package mcpcli implements the CLI mirror commands for the MCP-tool family
// (workstream 3.6-A — thin cmd/kern): the `kern pre-edit`, `prompt-fill`,
// `semantic-diff`, `semantic-merge`, `evidence-anchor`, `context-watch`,
// `ast-transform` and `compose` / `kern meta --pipeline` entry points. Flag
// parsing, usage guards and rendering live here; the tool bodies are the
// Compose / Analyze / Fill / Tool / Handle / Transform functions in the
// internal/mcp/<leaf> packages.
//
// The family landed as a TOP-LEVEL sibling rather than inside the
// internal/mcp/<leaf> packages because the internal/mcp monolith subtree is
// ratcheted at a 19,000 LOC cap and was already within ~600 LOC of it — the
// ARCHITECTURE.md contract prescribes exactly this extraction shape for
// anything that would grow the subtree (the mcp→mcpserve and mcp→metaroute
// pattern): mirrors landing inside a leaf cannot relieve the subtree, mirrors
// outside it do not count against it.
package mcpcli

import (
	"fmt"
	"os"
)

// exitError is the sentinel panic type used by fatal and fatalUsage — the
// cmd/kern helpers.go convention, preserved verbatim. It is recovered inside
// this package by run, which converts it back into the process exit code.
type exitError struct{ code int }

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

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
