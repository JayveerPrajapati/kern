package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/heal"
	mcpexec "github.com/JayveerPrajapati/kern/internal/mcp/exec"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/validate"
)

func (s *Server) handleSandbox(ctx context.Context, id string, args map[string]any) (string, error) {
	return mcpexec.Sandbox(ctx, id, args)
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
		task := argString(args, "task")
		if task == "" {
			task = "Fix the failing build/test/syntax errors in this project."
		}
		// kern_heal drives validation/build commands (arbitrary host code); it
		// must pass the governance firewall, fail closed. The task text is the
		// best command binding available (the concrete commands are
		// LLM-chosen), so an approval for one heal task never authorizes a
		// different one.
		if err := governance.CheckExecCommand(task, root); err != nil {
			return "", err
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
		res := heal.Run(ctx, root, task, model, rounds, timeout, argBool(args, "force"))
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
		// denial (same gate as kern_exec/kern_sandbox). The concrete command is
		// bound to any approval.
		if err := governance.CheckExecCommand(strings.Join(append([]string{c.Cmd}, c.Args...), " "), root); err != nil {
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
	return mcpexec.RunBuild(ctx, id, args)
}

func (s *Server) handleExec(ctx context.Context, args map[string]any) (string, error) {
	return mcpexec.Exec(ctx, args)
}
