package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	kdiff "github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	// firewall, fail closed (same gate as the MCP tools). The concrete command
	// is bound to any approval, so a HIGH/CRITICAL denial prints a resolvable
	// approval ID (`kern approve <id>`).
	root := f.root
	if root == "" {
		root = "."
	}
	if err := governance.CheckExecCommand(cmdStr, root); err != nil {
		fatal("Build: %v", err)
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
		// point at the timeout knob instead of a bare error. Child stderr
		// often lacks a trailing newline — separate it from kern's message.
		out := strings.TrimRight(res.Output, "\n")
		if out != "" {
			fmt.Println(out)
		}
		fatal("build: command failed (raise with --timeout N; --timeout 0 = no limit)")
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
		fatal("Validate: %v", err)
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
			fatal("sandbox command failed — see the JSON result above")
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
		fatal("validation FAILED (%s, %s)", c.Name, res.Err)
	}
	fatal("validation FAILED (%s, exit %d)", c.Name, res.ExitCode)

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
	// Pre-flight the LLM provider so a dead backend fails fast instead of
	// hanging the loop (observed: >60s with zero output and an orphaned
	// `opencode run` child). An explicit --llm selects an Ollama model and
	// must NOT let the auto chain fall through to agent CLIs (claude/
	// opencode/...) when Ollama is down; without --llm, probe the whole
	// chain exactly like `kern do` so a missing provider is a one-line
	// error rather than a silent hang.
	if f.llm != "" {
		c := llm.New("")
		if !c.Available() {
			fatal("heal: --llm %s selects an Ollama model but Ollama is not reachable at %s (start ollama or drop --llm to use the provider chain)", f.llm, c.Base)
		}
	} else if err := probeLLMProvider(); err != nil {
		fatal("heal: no reachable LLM provider: %v — start ollama (or set KERN_LLM_PROVIDER to a reachable provider) before using kern heal", err)
	}
	if f.file != "" {
		fmt.Printf("kern: heal %s (cmd=%s, rounds=%d, file=%s)\n", root, f.llm, iters, f.file)
	} else {
		fmt.Printf("kern: heal %s (cmd=%s, rounds=%d)\n", root, f.llm, iters)
	}
	var res *heal.Result
	// Cancel the agentic loop on SIGINT/SIGTERM so the spawned `opencode run`
	// child (exec.CommandContext in internal/llm/localcli.go) is killed too —
	// otherwise the grandchildren survive the interrupt and burn CPU.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.file != "" {
		res = heal.RunFile(ctx, root, task, f.llm, f.file, iters, toolTimeout(f), f.force)
	} else {
		res = heal.Run(ctx, root, task, f.llm, iters, toolTimeout(f), f.force)
	}
	if res.Err != nil {
		fatal("%v", res.Err)
	}
	if len(res.Unvalidated) > 0 {
		if res.LastOutput != "" {
			fmt.Print(res.LastOutput)
		}
		fatal("heal: unable to validate: %s (not OK — install the toolchain or scope with --file)", strings.Join(res.Unvalidated, ", "))
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
	fmt.Print(res.LastOutput)
	fatal("heal: still failing after %d round(s)", res.Iterations)

}

func runUdiff(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
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
		root := f.root
		if root == "" {
			root = "."
		}
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
	// governance firewall, fail closed. The concrete command is bound to any
	// approval, so a HIGH/CRITICAL denial prints a resolvable approval ID
	// (`kern approve <id>`).
	if err := governance.CheckExecCommand(strings.Join(cmdParts, " "), root); err != nil {
		fatal("Sandbox: %v", err)
	}
	if f.json {
		res := sandbox.RunGuarded(context.Background(), root, cmdParts[0], cmdParts[1:], toolTimeout(f), f.force)
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
	res := sandbox.RunGuarded(context.Background(), root, cmdParts[0], cmdParts[1:], toolTimeout(f), f.force)
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
	// Script source: positional args win. A lone "-" or a piped stdin
	// reads the script from stdin; a path to an existing file runs that
	// file (language from extension); anything else is treated as inline
	// code.
	var code, path string
	switch {
	case len(args) > 0 && args[0] == "-":
		b, err := readStdin()
		if err != nil {
			fatal("Exec: %v", err)
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
	root := f.root
	if root == "" {
		root = "."
	}
	if err := governance.CheckExecCommand(binding, root); err != nil {
		fatal("Exec: %v", err)
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
	if f.json {
		printJSON(res)
		if res.Err != nil {
			fatal("exec: %v", res.Err)
		}
		return
	}
	fmt.Print(res.Stdout)
	if !strings.HasSuffix(res.Stdout, "\n") && res.Stdout != "" {
		fmt.Println()
	}
	if res.Err != nil {
		fatal("exec: %v", res.Err)
	}
	fmt.Fprintf(os.Stderr, "kern exec: %s ok (%s, %d bytes stdout)\n", res.Runtime, res.Duration.Round(time.Millisecond), len(res.Stdout))

}
