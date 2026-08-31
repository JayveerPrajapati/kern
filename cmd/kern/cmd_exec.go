package main

import (
	"context"
	"fmt"
	kdiff "github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"os"
	"strings"
	"time"
)

func runBuild(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	cmdStr := strings.Join(args, " ")
	if cmdStr == "" {
		fatalUsage("usage: kern build <command>")
	}
	// Building runs arbitrary host commands; it must pass the governance
	// firewall, fail closed (same gate as the MCP tools).
	if err := governance.CheckExec(); err != nil {
		fatal("%v", err)
	}
	wireRecorder()
	ctx := context.Background()
	if tt := toolTimeout(f); tt > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tt)
		defer cancel()
	}
	res, err := optimize.RunBuild(ctx, cmdStr, f.dir, optimize.Options{Session: f.session})
	if err != nil {
		// RunBuild folds the error text into res.Output; print the partial
		// output (usually the actual compile/test error) before exiting, and
		// point at the timeout knob instead of a bare error.
		fmt.Print(res.Output)
		fatal("\nkern build: command failed (raise with --timeout N; --timeout 0 = no limit)")
	}
	fmt.Println(res.Output)

}

func runValidate(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" && len(args) > 0 {
		root = args[0]
	}
	if root == "" {
		root = "."
	}
	// Validation runs the detected or user-supplied command (arbitrary host
	// code); it must pass the governance firewall, fail closed.
	if err := governance.CheckExec(); err != nil {
		fatal("%v", err)
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
			fatal("%v", err)
		}
	}
	if f.json {
		res := validate.Run(context.Background(), root, c, toolTimeout(f))
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
			panic(exitError{code: 1})
		}
		return
	}
	fmt.Printf("kern: running %s %s\n", c.Cmd, strings.Join(c.Args, " "))
	res := validate.Run(context.Background(), root, c, toolTimeout(f))
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
		fmt.Printf("kern: validation FAILED (%s, %s)\n", c.Name, res.Err)
	} else {
		fmt.Printf("kern: validation FAILED (%s, exit %d)\n", c.Name, res.ExitCode)
	}
	panic(exitError{code: 1})

}

func runHeal(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	task := f.task
	if task == "" {
		task = "Fix the failing build/test/syntax errors in this project."
	}
	iters := f.max
	if iters == 0 {
		iters = 3
	}
	fmt.Printf("kern: heal %s (cmd=%s, rounds=%d)\n", root, f.llm, iters)
	res := heal.Run(context.Background(), root, task, f.llm, iters, toolTimeout(f))
	if res.Err != nil {
		fatal("%v", res.Err)
	}
	if res.Validated {
		fmt.Printf("kern: validated OK after %d correction round(s)\n", res.Iterations)
		if len(res.Changes) > 0 {
			fmt.Printf("kern: changed files (review and apply in your tree):\n")
			for _, c := range res.Changes {
				fmt.Printf("  - %s\n", c)
			}
		}
		if res.Diff != "" {
			fmt.Println(res.Diff)
		}
		return
	}
	fmt.Printf("kern: still failing after %d round(s)\n", res.Iterations)
	fmt.Print(res.LastOutput)
	panic(exitError{code: 1})

}

func runUdiff(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 2 {
		fatalUsage("usage: kern udiff <file-a> <file-b> [--out out.patch]")
	}
	ab, err := os.ReadFile(args[0])
	if err != nil {
		fatal("cannot read %s: %v", args[0], err)
	}
	bb, err := os.ReadFile(args[1])
	if err != nil {
		fatal("cannot read %s: %v", args[1], err)
	}
	u := kdiff.Unified(args[0], args[1], splitLines(string(ab)), splitLines(string(bb)))
	if u == "" {
		fmt.Println("files identical")
		return
	}
	if f.out != "" {
		if err := os.WriteFile(f.out, []byte(u), 0o644); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("wrote diff to %s\n", f.out)
		return
	}
	fmt.Print(u)

}

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
	f, args, err := parseFlags(flagArgs)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
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
	// governance firewall, fail closed.
	if err := governance.CheckExec(); err != nil {
		fatal("%v", err)
	}
	if f.json {
		res := sandbox.Run(context.Background(), root, cmdParts[0], cmdParts[1:], toolTimeout(f))
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
		})
		if !res.OK {
			panic(exitError{code: 1})
		}
		return
	}
	fmt.Printf("kern: sandbox run in %s: %s\n", root, strings.Join(cmdParts, " "))
	res := sandbox.Run(context.Background(), root, cmdParts[0], cmdParts[1:], toolTimeout(f))
	fmt.Print(res.Output)
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
		fmt.Printf("kern: FAILED (%s, %s); tree restored to snapshot (%d files)\n", reason, res.Duration.Round(time.Millisecond), res.Snapshots)
	} else {
		fmt.Printf("kern: FAILED (%s)\n", reason)
	}
	panic(exitError{code: 1})

}

func runExec(rest []string) {
	if len(rest) > 0 && rest[0] == "--list" {
		fmt.Printf("kern exec: available runtimes: %s\n", strings.Join(script.Available(), ", "))
		fmt.Printf("  supported languages: %s\n", strings.Join(script.Languages(), ", "))
		return
	}
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// Executing a script runs arbitrary code; it must pass the governance
	// firewall, fail closed (same gate as the MCP kern_exec tool).
	if err := governance.CheckExec(); err != nil {
		fatal("%v", err)
	}
	// Script source: positional args win. A lone "-" or a piped stdin
	// reads the script from stdin; a path to an existing file runs that
	// file (language from extension); anything else is treated as inline
	// code.
	var code, path string
	switch {
	case len(args) > 0 && args[0] == "-":
		b, err := readStdin()
		if err != nil {
			fatal("%v", err)
		}
		code = string(b)
	case len(args) > 0:
		if fi, err := os.Stat(args[0]); err == nil && !fi.IsDir() {
			path = args[0]
		} else {
			code = strings.Join(args, " ")
		}
	default:
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			b, rerr := readStdin()
			if rerr != nil {
				fatal("%v", rerr)
			}
			code = string(b)
		}
	}
	if strings.TrimSpace(code) == "" && path == "" {
		fatalUsage("usage: kern exec \"<code>\" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]\n       kern exec script.py | kern exec - | kern exec --list")
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
			b, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			run.Stdin = string(b)
		} else {
			b, err := os.ReadFile(f.stdin)
			if err != nil {
				fatal("%v", err)
			}
			run.Stdin = string(b)
		}
	}
	res := script.RunScript(run)
	if f.json {
		printJSON(res)
		if res.Err != nil {
			panic(exitError{code: 1})
		}
		return
	}
	fmt.Print(res.Stdout)
	if !strings.HasSuffix(res.Stdout, "\n") && res.Stdout != "" {
		fmt.Println()
	}
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "kern exec: "+res.Err.Error())
		panic(exitError{code: 1})
	}
	fmt.Fprintf(os.Stderr, "kern exec: %s ok (%s, %d bytes stdout)\n", res.Runtime, res.Duration.Round(time.Millisecond), len(res.Stdout))

}
