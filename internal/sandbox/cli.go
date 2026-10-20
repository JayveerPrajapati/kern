package sandbox

// cli.go implements the `kern sandbox` CLI entry point — extracted from
// cmd/kern/cmd_exec.go (workstream 3.6-A — exec/sandbox/validate/udiff/heal
// family extraction) following the internal/verifycmd precedent: this file
// owns the family's flag parsing, rendering and the exported RunSandbox entry
// point that returns the process exit code (int) to the cmd/kern dispatch
// table.
//
// Exit-code contract (pinned by the moved cmd/kern tests, must survive
// EXACTLY):
//
//	usage errors   -> 2 (no stack trace)
//	runtime errors -> 1
//	policy denials -> 3 (decided-state / policy-outcome)
//
// The command body keeps the house fatal style (panic with the exitError
// sentinel); RunSandbox recovers the sentinel and converts it back into the
// exit code, so cmd/kern's main() never sees a foreign panic and the dispatch
// table always receives a plain int.
//
// SECURITY: the governance gating is preserved verbatim from cmd/kern —
// the sandboxed command string is bound to any approval via
// governance.CheckExecCommand before it runs (fail closed). Neither a call
// was added nor one removed, and the CLI path remains operator-trusted
// exactly as before. Isolation is provided by the RunGuarded runner below.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// exitError is the sentinel panic type used by fatal, fatalUsage and
// fatalPolicy — the cmd/kern helpers.go convention, preserved verbatim. It is
// recovered inside this package by run (the exported entrypoint wrapper),
// which converts it back into the process exit code.
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
// returning its code. Non-sentinel panics are re-panicked (a genuine bug
// must not be swallowed into an exit code).
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

// RunSandbox executes `kern sandbox [root] -- <command...>` — run a command
// with snapshot rollback. It returns the process exit code (0 ok, 1 runtime
// failure, 2 usage error, 3 policy denial).
func RunSandbox(rest []string) int {
	return run(func() int { runSandbox(rest); return 0 })
}

// sandboxFlags holds the parsed `kern sandbox` flags the command reads —
// the cmd/kern unified flags struct's subset for this command family. The
// parser below replicates parseFlags' documented semantics for exactly these
// flags (single-dash long forms normalize to double-dash, --flag=value and
// --flag value both work, bool flags accept --flag/--flag=true/--flag=false,
// a trailing value-less flag is accept-and-default, and unknown flags are a
// sticky parse error).
type sandboxFlags struct {
	root       string
	json       bool
	force      bool
	timeout    int
	timeoutDur string
	timeoutSet bool
}

// parseSandboxFlags parses the sandbox family's flags from rest, replicating
// the unified cmd/kern parser's semantics for the flags runSandbox reads (and
// -h/--help, which the CLI consumes before dispatch), via the shared
// cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error). The `--` separator has already
// been split off by the command preamble.
func parseSandboxFlags(args []string) (sandboxFlags, []string, error) {
	var f sandboxFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--force":
			p.SetBool(&f.force, "--force", inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--help", "-h":
			// consumed before dispatch by the CLI; never read here
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseSandboxFlagsOrDie(rest []string) (sandboxFlags, []string) {
	f, args, err := parseSandboxFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

// cliProjectRoot returns the command's --root value, defaulting to the
// current directory — the root-resolution preamble every subcommand repeats
// (cmd/kern's projectRoot helper, replicated).
func cliProjectRoot(root string) string {
	if root != "" {
		return root
	}
	return "."
}

// isDir reports whether p exists and is a directory (cmd/kern's isDir,
// replicated).
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// ---- JSON rendering (cmd/kern printJSON, replicated) ----

// printJSON renders v as indented JSON on stdout — the sandbox family's
// --json output. Same contract as the cmd/kern helper it replaces: a marshal
// failure is a fatal error, and oversized string fields are clipped by
// clipJSONStrings before marshaling.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// ---- command body (moved verbatim from cmd/kern/cmd_exec.go) ----

func runSandbox(rest []string) {
	// A `--` separator ends kern flag parsing: everything after it is the
	// sandboxed command, verbatim. Without this split, parseFlags treats
	// shell flags like `-c` as unknown kern flags and the command can never
	// run. kern flags (e.g. --json) must come before the separator; a root
	// directory may come before or after it.
	var flagArgs, cmdRest []string
	separator := false
	for i, a := range rest {
		if a == "--" {
			flagArgs, cmdRest = rest[:i], rest[i+1:]
			separator = true
			break
		}
	}
	if !separator {
		flagArgs = rest // no separator: parse everything (legacy behavior)
	}
	f, args := parseSandboxFlagsOrDie(flagArgs)
	root := "."
	cmdParts := cmdRest
	if len(args) > 0 && args[0] != "--" && isDir(args[0]) {
		// A directory positional (root) precedes the command.
		root = args[0]
		if cmdParts == nil {
			cmdParts = args[1:]
		}
	} else if cmdParts == nil {
		cmdParts = args
	}
	if len(cmdParts) == 0 {
		fatalUsage("usage: kern sandbox [root] -- <command...>")
	}
	// When a single argument contains spaces (e.g. `kern sandbox "echo hello"`),
	// split it into tokens so the command is parsed as program + args rather
	// than treated as one literal executable name. This matches user intuition:
	// `kern sandbox "echo hello"` should work like `kern sandbox -- echo hello`.
	if len(cmdParts) == 1 && strings.Contains(cmdParts[0], " ") {
		cmdParts = strings.Fields(cmdParts[0])
	}
	// Sandboxed commands execute arbitrary host code; they must pass the
	// governance firewall, fail closed. The concrete command is bound to any
	// approval, so a HIGH/CRITICAL denial prints a resolvable approval ID
	// (`kern approve <id>`).
	if err := governance.CheckExecCommand(strings.Join(cmdParts, " "), root); err != nil {
		fatalPolicy("sandbox: execution denied by governance: %v", err)
	}
	if f.json {
		res := RunGuarded(context.Background(), root, cmdParts[0], cmdParts[1:], cliflags.TimeoutSeconds(f.timeoutSet, f.timeout), f.force)
		errStr := ""
		if res.Err != nil {
			errStr = res.Err.Error()
		}
		printJSON(map[string]any{
			"ok":          res.OK,
			"exit_code":   res.ExitCode,
			"error":       errStr,
			"duration_ms": res.Duration.Milliseconds(),
			"restored":    res.Restored,
			"snapshots":   res.Snapshots,
			"output":      res.Output,
			"network":     res.Network.Summary(),
		})
		if !res.OK {
			fatal("sandbox command failed — see the JSON result above")
		}
		return
	}
	fmt.Printf("kern: sandbox run in %s: %s\n", root, strings.Join(cmdParts, " "))
	// The CLI prints the sandbox output raw (no pii.Mask, no truncation): a
	// human at the terminal terminates the output and sees it in full, so
	// masking here would only corrupt legitimate content. The agent-facing
	// MCP surface (internal/mcp/exec.Sandbox) masks + truncates before
	// returning — that is where the confidentiality boundary lives.
	res := RunGuarded(context.Background(), root, cmdParts[0], cmdParts[1:], cliflags.TimeoutSeconds(f.timeoutSet, f.timeout), f.force)
	fmt.Print(res.Output)
	if res.Network != nil {
		fmt.Printf("kern: network: %s\n", res.Network.Summary())
	}
	if res.OK {
		fmt.Printf("kern: succeeded (%s); changes kept\n", res.Duration.Round(time.Millisecond))
		return
	}
	// Fold the failure reason into the verdict (see runValidate): a
	// timeout/cancel sets res.Err (ExitCode -1), a real non-zero exit sets
	// res.ExitCode. Keeping the reason in the verdict avoids losing it when
	// stdout and stderr are merged or redirected.
	reason := fmt.Sprintf("exit %d", res.ExitCode)
	if res.Err != nil {
		reason = res.Err.Error()
	}
	if res.Restored {
		fatal("FAILED (%s, %s); tree restored to snapshot (%d files)", reason, res.Duration.Round(time.Millisecond), res.Snapshots)
	}
	fatal("FAILED (%s)", reason)

}
