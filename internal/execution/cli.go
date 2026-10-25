package execution

// cli.go implements the `kern exec` family CLI entry points — exec, build,
// validate and udiff — extracted from cmd/kern/cmd_exec.go (workstream
// 3.6-A — exec/sandbox/validate/udiff/heal family extraction) following the
// internal/verifycmd precedent: this file owns the family's flag parsing,
// rendering and the exported Run* entry points that return the process exit
// code (int) to the cmd/kern dispatch table.
//
// Exit-code contract (pinned by the moved cmd/kern tests, must survive
// EXACTLY):
//
//	usage errors  -> 2 (no stack trace)
//	runtime errors -> 1
//	policy denials -> 3 (decided-state / policy-outcome)
//
// The command bodies keep the house fatal style (panic with the exitError
// sentinel); each exported Run* recovers the sentinel and converts it back
// into the exit code, so cmd/kern's main() never sees a foreign panic and
// the dispatch table always receives a plain int.
//
// SECURITY: the governance gating is preserved verbatim from cmd/kern —
// build/exec bind the concrete command text (or script content hash) to any
// approval via governance.CheckExecCommand, validate uses
// governance.CheckExec. Neither a call was added nor one removed, and the
// CLI path remains operator-trusted exactly as before.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	kdiff "github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/validate"
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

// RunExec executes `kern exec [flags] <code|path|->` — run code in an
// isolated sandbox. It returns the process exit code (0 ok, 1 runtime
// failure, 2 usage error).
func RunExec(rest []string) int {
	return run(func() int { runExec(rest); return 0 })
}

// RunBuild executes `kern build <command>` — run a build command in the
// governed sandbox. It returns the process exit code (0 ok, 1 runtime
// failure, 2 usage error, 3 policy denial).
func RunBuild(rest []string) int {
	return run(func() int { runBuild(rest); return 0 })
}

// RunValidate executes `kern validate [flags]` — auto-validate. It returns
// the process exit code (0 ok, 1 runtime failure, 2 usage error, 3 policy
// denial).
func RunValidate(rest []string) int {
	return run(func() int { runValidate(rest); return 0 })
}

// RunUdiff executes `kern udiff <file-a> <file-b> [flags]` — unified diff
// between files. It returns the process exit code (0 ok, 1 runtime failure,
// 2 usage error).
func RunUdiff(rest []string) int {
	return run(func() int { runUdiff(rest); return 0 })
}

// ---- flag parsing (cmd/kern parseFlags semantics, family subset) ----

// The parsers below replicate parseFlags' documented semantics for exactly
// the flags each command reads (and -h/--help, which the CLI consumes before
// dispatch): single-dash long forms normalize to double-dash, --flag=value
// and --flag value both work, bool flags accept --flag/--flag=true/
// --flag=false, a trailing value-less flag is accept-and-default, and unknown
// flags are a sticky parse error.

type execFlags struct {
	root       string
	lang       string
	stdin      string
	max        int
	timeout    int
	timeoutDur string
	timeoutSet bool
	json       bool
	readonly   bool   // --readonly: skip the KERN_TOOLS/KERN_ALLOW_EXEC allowlist gate (read-only bash fallbacks)
	execOutput string // --output (exec: summary|tail:N|lines:A-B|full)
	execAnchor string // --anchor (exec: re-slice a kept run without re-executing)
}

// parseExecFlags parses the exec family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runExec reads, via the
// shared cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error). --list is stripped by the caller's
// preamble (accepted in any position) and is not a parser flag — exactly as
// in cmd/kern.
func parseExecFlags(args []string) (execFlags, []string, error) {
	var f execFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--lang":
			p.SetStr(&f.lang, inline, hasInline)
		case "--stdin":
			p.SetStr(&f.stdin, inline, hasInline)
		case "--max":
			p.SetInt(&f.max, "--max", inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--readonly":
			p.SetBool(&f.readonly, "--readonly", inline, hasInline)
		case "--output":
			p.SetStr(&f.execOutput, inline, hasInline)
		case "--anchor":
			p.SetStr(&f.execAnchor, inline, hasInline)
		case "--help", "-h":
			// consumed before dispatch by the CLI; never read here
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseExecFlagsOrDie(rest []string) (execFlags, []string) {
	f, args, err := parseExecFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

type buildFlags struct {
	dir        string
	session    string
	root       string
	timeout    int
	timeoutDur string
	timeoutSet bool
}

// parseBuildFlags parses the build family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runBuild reads, via the
// shared cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error).
func parseBuildFlags(args []string) (buildFlags, []string, error) {
	var f buildFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--dir":
			p.SetStr(&f.dir, inline, hasInline)
		case "--session":
			p.SetStr(&f.session, inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseBuildFlagsOrDie(rest []string) (buildFlags, []string) {
	f, args, err := parseBuildFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

type validateFlags struct {
	cmd        string
	json       bool
	root       string
	timeout    int
	timeoutDur string
	timeoutSet bool
}

// parseValidateFlags parses the validate family's flags from rest,
// replicating the unified cmd/kern parser's semantics for the flags
// runValidate reads, via the shared cliflags.Parser (single-dash long forms
// normalize, --flag=value and --flag value both work, a trailing value-less
// flag is accept-and-default, unknown flags are a sticky parse error).
func parseValidateFlags(args []string) (validateFlags, []string, error) {
	var f validateFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--cmd":
			p.SetStr(&f.cmd, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseValidateFlagsOrDie(rest []string) (validateFlags, []string) {
	f, args, err := parseValidateFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

type udiffFlags struct {
	compact bool
	out     string
	root    string
}

// parseUdiffFlags parses the udiff family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runUdiff reads, via the
// shared cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error).
func parseUdiffFlags(args []string) (udiffFlags, []string, error) {
	var f udiffFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--compact":
			p.SetBool(&f.compact, "--compact", inline, hasInline)
		case "--out":
			p.SetStr(&f.out, inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseUdiffFlagsOrDie(rest []string) (udiffFlags, []string) {
	f, args, err := parseUdiffFlags(rest)
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

// ---- JSON rendering (cmd/kern printJSON, replicated) ----

// printJSON renders v as indented JSON on stdout — the exec family's --json
// outputs. Same contract as the cmd/kern helper it replaces: a marshal
// failure is a fatal error, and oversized string fields are clipped by
// clipJSONStrings before marshaling.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// ---- command bodies (moved verbatim from cmd/kern/cmd_exec.go) ----

func runBuild(rest []string) {
	f, args := parseBuildFlagsOrDie(rest)
	cmdStr := strings.Join(args, " ")
	if cmdStr == "" {
		fatalUsage("usage: kern build <command>")
	}
	// Shell command-substitution (backticks / $(...)) would be executed blind
	// by RunBuild's sh -c — observed live: `echo \`touch /tmp/x\`` created the
	// file. Reject BEFORE the governance check (the diagnostic must name the
	// construct even for an otherwise-allowable command) and before any
	// execution. Any occurrence rejects, including inside quoted literals:
	// write literal text (e.g. a commit message containing backticks) to a
	// file and pass it with -F instead.
	if containsCommandSubstitution(cmdStr) {
		fatalPolicy("build: execution denied: command contains shell command-substitution constructs (backtick / $(...) — kern build does not execute them blind; for quoted literals such as commit messages write the text to a file and pass it with -F")
	}
	// Building runs arbitrary host commands; it must pass the governance
	// firewall, fail closed (same gate as the MCP tools). The concrete command
	// is bound to any approval, so a HIGH/CRITICAL denial prints a resolvable
	// approval ID (`kern approve <id>`).
	root := cliProjectRoot(f.root)
	if err := governance.CheckExecCommand(cmdStr, root); err != nil {
		fatalPolicy("build: execution denied by governance: %v", err)
	}
	// Metrics recorder wiring (cmd/kern's wireRecorder → optimize.EnsureRecorder).
	_ = optimize.EnsureRecorder()
	ctx := context.Background()
	if tt := cliflags.TimeoutSeconds(f.timeoutSet, f.timeout); tt > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tt)
		defer cancel()
	}
	res, err := optimize.RunBuild(ctx, cmdStr, f.dir, optimize.Options{Session: f.session})
	if err != nil {
		// RunBuild folds the error text into res.Output; print the partial
		// output (usually the actual compile/test error) before exiting.
		// Child stderr often lacks a trailing newline — separate it from
		// kern's message.
		out := strings.TrimRight(res.Output, "\n")
		if out != "" {
			fmt.Println(out)
		}
		// Classify the failure so the exit code and the advice tell the
		// truth. A context deadline is the wrapper's own timeout — the
		// --timeout advice applies. A non-zero child exit PROPAGATES the
		// child's exit code (the make / sh -c convention: an agent
		// scripting `kern build <cmd>` must see the child's real code, not
		// a blanket 1 — observed live: `kern build kern twin` reported
		// "exit code 1" while the child exited 2). Anything else is a
		// wrapper failure (exit 1).
		if ctx.Err() != nil {
			fatal("build: command timed out (raise with --timeout N; --timeout 0 = no limit)")
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			if code <= 0 || code > 255 {
				code = 1 // signal-killed (negative) or out of range: plain failure
			}
			fmt.Fprintf(os.Stderr, "kern: build: command failed with exit code %d\n", code)
			panic(exitError{code: code})
		}
		fatal("build: %v", err)
	}
	fmt.Println(res.Output)

}

// containsCommandSubstitution reports whether a command string carries shell
// command-substitution constructs (backticks or $(...)). Presence alone
// rejects: RunBuild executes through sh -c, which would evaluate the
// substitution blind — so any occurrence (even inside a quoted literal) is
// refused, with a file-based workaround for literal text.
func containsCommandSubstitution(s string) bool {
	return strings.Contains(s, "`") || strings.Contains(s, "$(")
}

func runValidate(rest []string) {
	f, args := parseValidateFlagsOrDie(rest)
	root := cliProjectRoot(f.root)
	if f.root == "" && len(args) > 0 && args[0] != "" {
		root = args[0]
	}
	// Validation runs the detected or user-supplied command (arbitrary host
	// code); it must pass the governance firewall, fail closed.
	if err := governance.CheckExec(); err != nil {
		fatalPolicy("validate: execution denied by governance: %v", err)
	}
	var c *validate.Command
	if f.cmd != "" {
		parts := strings.Fields(f.cmd)
		if len(parts) == 0 {
			fatal("empty --cmd")
		}
		c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
	} else {
		var err error
		c, err = validate.Detect(root)
		if err != nil {
			fatal("Validate: %v", err)
		}
	}
	if f.json {
		res := validate.Run(context.Background(), root, c, cliflags.TimeoutSeconds(f.timeoutSet, f.timeout))
		errStr := ""
		if res.Err != nil {
			errStr = res.Err.Error()
		}
		printJSON(map[string]any{
			"command":     c.Name,
			"cmd":         c.Cmd,
			"args":        c.Args,
			"ok":          res.OK,
			"exit_code":   res.ExitCode,
			"error":       errStr,
			"duration_ms": res.Dur.Milliseconds(),
			"output":      res.Output,
		})
		if !res.OK {
			fatal("sandbox command failed — see the JSON result above")
		}
		return
	}
	fmt.Printf("kern: running %s %s\n", c.Cmd, strings.Join(c.Args, " "))
	res := validate.Run(context.Background(), root, c, cliflags.TimeoutSeconds(f.timeoutSet, f.timeout))
	fmt.Print(res.Output)
	if res.OK {
		fmt.Printf("kern: validation OK (%s, %s)\n", c.Name, res.Dur.Round(time.Millisecond))
		return
	}
	// Fold the failure reason into the verdict so it is not lost on a separate
	// stream: a timeout/cancel sets res.Err (with ExitCode -1) while a real
	// non-zero exit sets res.ExitCode. Reporting both keeps the verdict
	// self-contained whether stdout is merged with stderr or not.
	if res.Err != nil {
		fatal("validation FAILED (%s, %s)", c.Name, res.Err)
	}
	fatal("validation FAILED (%s, exit %d)", c.Name, res.ExitCode)

}

func runUdiff(rest []string) {
	f, args := parseUdiffFlagsOrDie(rest)
	if len(args) < 2 {
		fatalUsage("usage: kern udiff <file-a> <file-b> [--out out.patch] [--compact]")
	}
	ab, err := os.ReadFile(args[0])
	if err != nil {
		fatal("cannot read %s: %v", args[0], err)
	}
	bb, err := os.ReadFile(args[1])
	if err != nil {
		fatal("cannot read %s: %v", args[1], err)
	}
	if f.compact {
		// View-only compact diff: cannot combine with --out (not a patch).
		if f.out != "" {
			fatalUsage("--compact and --out cannot be combined")
		}
		root := cliProjectRoot(f.root)
		ix, _ := index.Load(root)
		u := kdiff.Compact(args[0], args[1], strutil.Lines(string(ab)), strutil.Lines(string(bb)), kdiff.IndexSpanResolver(ix))
		if u == "" {
			fmt.Println("files identical")
			return
		}
		fmt.Print(u)
		return
	}
	u := kdiff.Unified(args[0], args[1], strutil.Lines(string(ab)), strutil.Lines(string(bb)))
	if u == "" {
		fmt.Println("files identical")
		return
	}
	if f.out != "" {
		if err := os.WriteFile(f.out, []byte(u), 0o644); err != nil {
			fatal("Udiff: %v", err)
		}
		fmt.Printf("wrote diff to %s\n", f.out)
		return
	}
	fmt.Print(u)

}

// shellQuoteJoin joins command-line tokens into a single shell line, quoting
// any token whose raw text the shell would split or re-interpret, so the
// reconstructed line reproduces the original argv (6.2). Tokens made only of
// safe characters are emitted verbatim; everything else is wrapped in single
// quotes with embedded single quotes escaped the POSIX way ('\”).
func shellQuoteJoin(tokens []string) string {
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t != "" && !strings.ContainsAny(t, " \t\n&|;<>()$`'\"\\*?[]{}#!~") {
			quoted = append(quoted, t)
			continue
		}
		quoted = append(quoted, "'"+strings.ReplaceAll(t, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

func runExec(rest []string) {
	// A bare `--` ends kern flag parsing (the shell-idiom separator,
	// mirroring `kern sandbox [root] -- <command...>`): everything after it
	// is the script verbatim, so `kern exec -- go version` runs "go version"
	// instead of treating "--" as script text (6.2). kern flags (--lang,
	// --timeout, ...) must come before the separator.
	var flagArgs, scriptArgs []string
	separator := false
	for i, a := range rest {
		if a == "--" {
			flagArgs, scriptArgs = rest[:i], rest[i+1:]
			separator = true
			break
		}
	}
	if !separator {
		flagArgs = rest
	}
	// --list is a real flag, accepted in any position before the separator
	// (and in legacy no-separator runs): strip it before the shared parser
	// so `kern exec --lang py --list` or `kern exec script.py --list` list
	// the runtimes instead of failing with "unknown flag" (parseFlags has no
	// per-command registry for it). After a `--` separator "--list" is
	// script text, not a flag.
	list := false
	filtered := make([]string, 0, len(flagArgs))
	for _, a := range flagArgs {
		if a == "--list" {
			list = true
			continue
		}
		filtered = append(filtered, a)
	}
	if list {
		fmt.Printf("kern exec: available runtimes: %s\n", strings.Join(script.Available(), ", "))
		fmt.Printf("  supported languages: %s\n", strings.Join(script.Languages(), ", "))
		return
	}
	f, args := parseExecFlagsOrDie(filtered)
	// --anchor: re-slice a kept run WITHOUT re-executing anything, so this
	// branch must come before the code-required usage check and before the
	// governance gate (no code runs). --output shapes it (same contract as
	// kern_verify: summary|tail:N|lines:A-B|full).
	if f.execAnchor != "" {
		root := cliProjectRoot(f.root)
		full, err := LoadExecRun(root, f.execAnchor)
		if err != nil {
			fatal("anchor: %v", err)
		}
		out, serr := ShapeExecOutput(f.execOutput, full, ExecMeta{Anchor: f.execAnchor})
		if serr != nil {
			fatalUsage("exec: %v", serr)
		}
		fmt.Print(out)
		return
	}
	// Script source: positional args win. A lone "-" or a piped stdin
	// reads the script from stdin; a path to an existing file runs that
	// file (language from extension); anything else is treated as inline
	// code. With a `--` separator the script is exactly the tokens after it
	// (joined); without one the parser's positionals are the script.
	var code, path string
	scriptParts := args
	if separator {
		scriptParts = scriptArgs
	}
	switch {
	case len(scriptParts) > 0 && scriptParts[0] == "-":
		b, err := cliflags.ReadStdin()
		if err != nil {
			fatal("Exec: %v", err)
		}
		code = string(b)
	case len(scriptParts) > 0:
		if fi, err := os.Stat(scriptParts[0]); err == nil && !fi.IsDir() {
			path = scriptParts[0]
		} else if separator {
			// A `--` separator means the tokens ARE the command line: join
			// them with shell quoting so the reconstructed line reproduces
			// the original argv. Flat-joining would break grouped arguments
			// (`sh -c 'echo separator-ok'` -> "sh -c echo separator-ok",
			// where sh treats "separator-ok" as $0 and echo prints nothing).
			code = shellQuoteJoin(scriptParts)
		} else {
			code = strings.Join(scriptParts, " ")
		}
	default:
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			b, rerr := cliflags.ReadStdin()
			if rerr != nil {
				fatal("%v", rerr)
			}
			code = string(b)
		}
	}
	if strings.TrimSpace(code) == "" && path == "" {
		fatalUsage("usage: kern exec \"<code>\" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-] [--readonly] [--output full|summary|tail:N|lines:A-B]\n       kern exec --anchor <id> [--output ...] re-slices a kept run without re-executing\n       kern exec script.py | kern exec - | kern exec --list\n       --readonly: skip the KERN_TOOLS/KERN_ALLOW_EXEC allowlist gate (read-only bash fallbacks; firewall + masking still enforced)\n       isolation: Seatbelt (macOS) / netns (Linux) / fail-closed elsewhere; override: KERN_ALLOW_UNISOLATED=1")
	}
	// Executing a script runs arbitrary code; it must pass the governance
	// firewall, fail closed (same gate as the MCP kern_exec tool). The script
	// text (or the script file path, when a file is run) is bound to any
	// approval, so a HIGH/CRITICAL denial prints a resolvable approval ID
	// (`kern approve <id>`).
	binding := code
	if path != "" {
		// R5: bind to the script CONTENT, not the path — a path-bound
		// approval would let an approved script be edited and replayed. Read
		// the file and bind to a SHA-256 of its bytes so the approval covers
		// exactly what runs. An unreadable file falls back to the path (the
		// run itself will fail later).
		if b, rerr := os.ReadFile(path); rerr == nil {
			sum := sha256.Sum256(b)
			binding = hex.EncodeToString(sum[:])
		} else {
			binding = path
		}
	}
	root := cliProjectRoot(f.root)
	// --readonly (or KERN_READONLY_EXEC=1) routes read-only bash fallbacks
	// through CheckExecCommandReadonly: the KERN_TOOLS/KERN_ALLOW_EXEC
	// allowlist gate is skipped (a pure-read pipeline has no write/network/
	// destructive surface), but the risk firewall, rejection stickiness and
	// dangerous-class escalation still hold — GATE commands remain denied.
	if f.readonly || os.Getenv("KERN_READONLY_EXEC") == "1" {
		if err := governance.CheckExecCommandReadonly(binding, root); err != nil {
			fatalPolicy("exec: execution denied by governance: %v", err)
		}
	} else if err := governance.CheckExecCommand(binding, root); err != nil {
		fatalPolicy("exec: execution denied by governance: %v", err)
	}

	run := script.Run{Lang: f.lang, Code: code, Path: path}
	// 15s default so a runaway script can't hang an agent for the shared
	// --timeout default (120s); an explicit --timeout always wins — even
	// 120, which the old sentinel comparison misread as "unset". An explicit
	// "--timeout 0" means no limit (the toolTimeout contract); the script
	// runner turns Timeout <= 0 into its own 10s default, so translate 0 to a
	// 24h ceiling instead of passing it through.
	if f.timeoutSet {
		if f.timeout == 0 {
			run.Timeout = 24 * time.Hour
		} else {
			run.Timeout = time.Duration(f.timeout) * time.Second
		}
	} else {
		run.Timeout = 15 * time.Second
	}
	run.MaxOut = f.max
	if f.stdin != "" {
		if f.stdin == "-" {
			b, err := cliflags.ReadStdin()
			if err != nil {
				fatal("Exec: %v", err)
			}
			run.Stdin = string(b)
		} else {
			b, err := os.ReadFile(f.stdin)
			if err != nil {
				fatal("Exec: %v", err)
			}
			run.Stdin = string(b)
		}
	}
	res := script.RunScript(run)
	// Mask secrets/PII in script output before it reaches the terminal or
	// JSON — same posture as the MCP kern_exec tool (internal/mcp/
	// handlers_exec.go masks res.Stdout before returning). Without this,
	// CLI-driven automation piping `kern exec` output into prompts/contexts
	// silently lost the masking guarantee the MCP surface provides.
	masked := 0
	if res.Stdout != "" {
		m := pii.Mask(res.Stdout)
		res.Stdout = m.Text
		masked += m.Replaced
	}
	if res.Stderr != "" {
		m := pii.Mask(res.Stderr)
		res.Stderr = m.Text
		masked += m.Replaced
	}
	if masked > 0 {
		fmt.Fprintf(os.Stderr, "kern exec: masked %d secret(s) in script output (kern mask for details)\n", masked)
	}
	// Keep the full masked output (in-memory anchor + on-disk copy) so any
	// shaped call can be widened or re-windowed later without re-running:
	// --output summary|tail:N|lines:A-B (default full = exact, untouched).
	full := res.Stdout
	anchor := optimize.StoreAnchor(full)
	SaveExecRun(root, anchor, full)
	if f.execOutput != "" {
		shaped, serr := ShapeExecOutput(f.execOutput, full, ExecMeta{
			Runtime: res.Runtime,
			DurMS:   res.Duration.Milliseconds(),
			Exit:    res.ExitCode,
			Anchor:  anchor,
		})
		if serr != nil {
			fatalUsage("exec: %v", serr)
		}
		res.Stdout = shaped
	}
	if f.json {
		printJSON(res)
		if res.Err != nil {
			// an undetectable language is a MISSING
			// REQUIRED ARGUMENT (--lang), a usage error — exit 2, not the
			// runtime-error 1. The runtime/script errors stay at 1.
			if isUsageExecError(res.Err) {
				fatalUsage("exec: %v", res.Err)
			}
			fatal("exec: %v", res.Err)
		}
		return
	}
	fmt.Print(res.Stdout)
	if !strings.HasSuffix(res.Stdout, "\n") && res.Stdout != "" {
		fmt.Println()
	}
	if res.Err != nil {
		// an undetectable language is a MISSING REQUIRED
		// ARGUMENT (--lang), a usage error — exit 2, not the runtime-error 1.
		if isUsageExecError(res.Err) {
			fatalUsage("exec: %v", res.Err)
		}
		fatal("exec: %v", res.Err)
	}
	fmt.Fprintf(os.Stderr, "kern exec: %s ok (%s, %d bytes stdout)\n", res.Runtime, res.Duration.Round(time.Millisecond), len(res.Stdout))
	// Post-hoc steering (stderr on the CLI — stdout stays pipe-clean): a
	// script that statically reads files AND returned a large raw stdout is
	// a dump through the wrong door; the token-cheap surfaces are
	// kern_explore/kern_search, and shaping is one --output away.
	if footer := DumpSteer(code, full, anchor); footer != "" {
		fmt.Fprintln(os.Stderr, footer)
	}
}

// isUsageExecError classifies a script-run error as a usage error (exit 2)
// vs a runtime error (exit 1). An undetectable language is a MISSING REQUIRED
// ARGUMENT (--lang) — the caller's fault, so it maps to the usage exit like
// every other missing-required-arg case.
func isUsageExecError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot detect language")
}
