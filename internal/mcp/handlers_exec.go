package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"os"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleSandbox(ctx context.Context, id string, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		cmdLine := argString(args, "command")
		if cmdLine == "" {
			return "", fmt.Errorf("command is required")
		}
		// Host command execution must pass the governance firewall; fail closed on denial.
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		stop := s.startProgress(ctx, id, "kern_sandbox")
		defer stop()
		parts := splitShellLine(cmdLine)
		if len(parts) == 0 {
			return "", fmt.Errorf("command is empty")
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		res := sandbox.Run(ctx, root, parts[0], parts[1:], timeout)
		var b strings.Builder
		if res.OK {
			fmt.Fprintf(&b, "status: PASS (%s), changes kept\n", res.Duration.Round(time.Millisecond))
		} else if res.Restored {
			fmt.Fprintf(&b, "status: FAIL (exit %d, %s), tree restored to snapshot (%d files)\n", res.ExitCode, res.Duration.Round(time.Millisecond), res.Snapshots)
		} else {
			fmt.Fprintf(&b, "status: FAIL (exit %d, %s)\n", res.ExitCode, res.Duration.Round(time.Millisecond))
		}
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		if res.Network != nil {
			fmt.Fprintf(&b, "network: %s\n", res.Network.Summary())
		}
		out := res.Output
		if len(out) > 4000 {
			out = out[:4000] + "\n... (truncated)"
		}
		if out != "" {
			// Mask PII/secrets in command output before returning it to the caller.
			out = pii.Mask(out).Text
			fmt.Fprintf(&b, "output:\n%s\n", out)
		}
		if len(res.Manifest) > 0 {
			fmt.Fprintf(&b, "=== sandbox impact manifest ===\n")
			for _, c := range res.Manifest {
				marker := "~"
				switch c.Kind {
				case "created":
					marker = "+"
				case "deleted":
					marker = "-"
				}
				fmt.Fprintf(&b, "%s %s (%d B, sha256:%s)\n", marker, c.Path, c.Size, c.Hash)
			}
			fmt.Fprintf(&b, "%d change(s)", len(res.Manifest))
			if m := len(res.SkippedFiles); m > 0 {
				fmt.Fprintf(&b, "; %d file(s) skipped (over snapshot cap)", m)
			}
			if res.Restored {
				fmt.Fprintf(&b, "; tree restored to snapshot — changes rolled back")
			}
			fmt.Fprintf(&b, "\n")
		}
		return b.String(), nil

	}
}

func (s *Server) handleDiffFiles(ctx context.Context, args map[string]any) (string, error) {
	{
		a := argString(args, "a")
		b := argString(args, "b")
		if a == "" || b == "" {
			return "", fmt.Errorf("a and b are required")
		}
		root := argString(args, "root")
		ap, err := rootedPath(root, a)
		if err != nil {
			return "", err
		}
		bp, err := rootedPath(root, b)
		if err != nil {
			return "", err
		}
		ab, err := os.ReadFile(ap)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", a, err)
		}
		bb, err := os.ReadFile(bp)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", b, err)
		}
		var u string
		if argBool(args, "compact") {
			// Compact mode: view-only diff with collapsed context runs,
			// annotated with the enclosing symbol when the index resolves it.
			// A failed index load just means no span annotations.
			ix, _ := s.loadIndex(ctx, root)
			u = diff.Compact(a, b, strutil.Lines(string(ab)), strutil.Lines(string(bb)), diff.IndexSpanResolver(ix))
		} else {
			u = diff.Unified(a, b, strutil.Lines(string(ab)), strutil.Lines(string(bb)))
		}
		if u == "" {
			return "files identical", nil
		}
		return u, nil

	}
}

func (s *Server) handleHeal(ctx context.Context, id string, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		// kern_heal drives validation/build commands (arbitrary host code); it
		// must pass the governance firewall, fail closed.
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		task := argString(args, "task")
		if task == "" {
			task = "Fix the failing build/test/syntax errors in this project."
		}
		model := argString(args, "model")
		rounds := 3
		if s := argString(args, "max_rounds"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("max_rounds: invalid integer %q", s)
			}
			if n > 0 {
				rounds = n
			}
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		stop := s.startProgress(ctx, id, "kern_heal")
		defer stop()
		res := heal.Run(ctx, root, task, model, rounds, timeout)
		var b strings.Builder
		if res.Validated {
			fmt.Fprintf(&b, "status: healed OK after %d round(s)\n", res.Iterations)
			for _, c := range res.Changes {
				fmt.Fprintf(&b, "changed: %s\n", c)
			}
			if res.Diff != "" {
				fmt.Fprintf(&b, "diff:\n%s\n", res.Diff)
			}
			return b.String(), nil
		}
		fmt.Fprintf(&b, "status: still failing after %d round(s)\n", res.Iterations)
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		if res.LastOutput != "" {
			fmt.Fprintf(&b, "output:\n%s\n", truncateMCP(res.LastOutput, 3000))
		}
		return b.String(), nil
	}
}

func (s *Server) handleValidate(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		var c *validate.Command
		if cmd := argString(args, "command"); cmd != "" {
			parts := strings.Fields(cmd)
			if len(parts) == 0 {
				return "", fmt.Errorf("command is empty")
			}
			c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
		} else {
			var err error
			c, err = validate.Detect(root)
			if err != nil {
				return "", err
			}
		}
		// Running the detected or user-supplied command executes arbitrary host
		// code, so it must pass the governance firewall first; fail closed on
		// denial (same gate as kern_exec/kern_sandbox).
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		res := validate.Run(ctx, root, c, timeout)
		var b strings.Builder
		fmt.Fprintf(&b, "command: %s %s\n", c.Cmd, strings.Join(c.Args, " "))
		fmt.Fprintf(&b, "status: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[res.OK])
		fmt.Fprintf(&b, "exit: %d\n", res.ExitCode)
		fmt.Fprintf(&b, "duration: %s\n", res.Dur.Round(time.Millisecond))
		out := res.Output
		if len(out) > 4000 {
			out = out[:4000] + "\n... (truncated)"
		}
		if out != "" {
			fmt.Fprintf(&b, "output:\n%s\n", out)
		}
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		return b.String(), nil

	}
}

func (s *Server) handleRunBuild(ctx context.Context, id string, args map[string]any) (string, error) {
	{
		cmd := argString(args, "command")
		if cmd == "" {
			return "", fmt.Errorf("command is required")
		}
		// Host command execution must pass the governance firewall; fail closed
		// on denial (same gate as kern_exec/kern_sandbox).
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		stop := s.startProgress(ctx, id, "kern_run_build")
		defer stop()
		// Bound the build so a hanging command cannot hold the server: builds
		// and tests can legitimately take minutes, but never forever.
		bctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		res, err := optimize.RunBuild(bctx, cmd, argString(args, "dir"), optimize.Options{})
		if err != nil {
			// Fold partial output into the result: RunBuild already appends the
			// error text to res.Output, and on a timeout that partial output is
			// usually the actual compiler/test error — discarding it (as the
			// previous bare error return did) hides the only useful signal.
			return res.Output, err
		}
		return res.Output, nil

	}
}

func (s *Server) handleExec(ctx context.Context, args map[string]any) (string, error) {
	{
		if argString(args, "list") == "true" || argString(args, "list") == "1" {
			return fmt.Sprintf("installed runtimes: %s\nsupported languages: %s",
				strings.Join(script.Available(), ", "), strings.Join(script.Languages(), ", ")), nil
		}
		// Running a script executes an arbitrary host command, so it must pass
		// the governance firewall first; fail closed on denial.
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		// no_isolate (full env + network) is only honored when the operator has
		// explicitly set KERN_ALLOW_NO_ISOLATE=1; otherwise it is ignored.
		noIsolate := argString(args, "no_isolate") == "true" || argString(args, "no_isolate") == "1"
		if noIsolate && os.Getenv("KERN_ALLOW_NO_ISOLATE") == "" {
			noIsolate = false
		}
		run := script.Run{
			Lang:      argString(args, "lang"),
			Code:      argString(args, "code"),
			Stdin:     argString(args, "stdin"),
			NoIsolate: noIsolate,
		}
		if v := argString(args, "timeout"); v != "" {
			sec, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", v)
			}
			run.Timeout = time.Duration(sec) * time.Second
		}
		if v := argString(args, "max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("max: invalid integer %q", v)
			}
			if n > 0 {
				run.MaxOut = n
			}
		}
		res := script.RunScript(run)
		if res.Err != nil {
			return "", fmt.Errorf("kern_exec: %s", res.Err)
		}
		// Mask PII/secrets in script stdout before returning it.
		return pii.Mask(res.Stdout).Text, nil

	}
}
