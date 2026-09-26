// Package exec owns command execution, guarded sandbox execution, build
// runners, file diffing, validation and self-healing (kern_exec,
// kern_sandbox, kern_diff_files, kern_validate, kern_heal) as plain
// functions. RunBuild backs the raw=true mode of kern_validate.
package exec

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/script"
)

func splitShellLine(line string) []string {
	var parts []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// maskedOutput masks secrets in the FULL sandbox output, then truncates for
// display. Masking MUST run before truncation: a secret straddling the byte
// cut would otherwise escape the mask regexes — the mask would only ever see
// the truncated head, and the tail of the secret would pass through
// unmasked to the agent context.
func maskedOutput(out string, limit int) string {
	out = pii.Mask(out).Text
	if len(out) > limit {
		out = out[:limit] + "\n... (truncated)"
	}
	return out
}

// Sandbox runs a command in an isolated snapshot worktree.
func Sandbox(ctx context.Context, id string, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	cmdLine := mcpargs.ArgString(args, "command")
	if cmdLine == "" {
		return "", fmt.Errorf("command is required")
	}

	if err := governance.CheckExecCommand(cmdLine, root); err != nil {
		return "", err
	}
	parts := splitShellLine(cmdLine)
	if len(parts) == 0 {
		return "", fmt.Errorf("command is empty")
	}
	timeout := 120 * time.Second
	if s := mcpargs.ArgString(args, "timeout"); s != "" {
		sec, err := strconv.Atoi(s)
		if err != nil {
			return "", fmt.Errorf("timeout: invalid integer %q", s)
		}
		if sec > 0 {
			timeout = time.Duration(sec) * time.Second
		}
	}
	res := sandbox.RunGuarded(ctx, root, parts[0], parts[1:], timeout, mcpargs.ArgBool(args, "force"))
	var b strings.Builder
	if res.OK {
		fmt.Fprintf(&b, "status: PASS (%s), changes kept\n", res.Duration.Round(time.Millisecond))
	} else if res.Restored {
		fmt.Fprintf(&b, "status: FAIL (exit %d, %s), tree restored to snapshot (%d files)\n", res.ExitCode, res.Duration.Round(time.Millisecond), res.Snapshots)
	} else {
		fmt.Fprintf(&b, "status: FAIL (exit %d, %s)\n", res.ExitCode, res.Duration.Round(time.Millisecond))
	}
	if res.Err != nil {
		fmt.Fprintf(&b, "error: %v\n", pii.Mask(res.Err.Error()).Text)
	}
	if res.Network != nil {
		fmt.Fprintf(&b, "network: %s\n", res.Network.Summary())
	}
	out := maskedOutput(res.Output, 4000)
	if out != "" {
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

// RunBuild executes a build or test command under an execution timeout.
func RunBuild(ctx context.Context, id string, args map[string]any) (string, error) {
	cmd := mcpargs.ArgString(args, "command")
	if cmd == "" {
		return "", fmt.Errorf("command is required")
	}
	dir := root.ResolveRoot(mcpargs.ArgString(args, "dir"))
	if err := governance.CheckExecCommand(cmd, dir); err != nil {
		return "", err
	}
	bctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	res, err := optimize.RunBuild(bctx, cmd, dir, optimize.Options{})
	if err != nil {
		return res.Output, err
	}
	return res.Output, nil
}

// Exec executes isolated scripts in supported runtimes (python, sh, node, etc.).
func Exec(ctx context.Context, args map[string]any) (string, error) {
	if mcpargs.ArgString(args, "list") == "true" || mcpargs.ArgString(args, "list") == "1" {
		return fmt.Sprintf("installed runtimes: %s\nsupported languages: %s",
			strings.Join(script.Available(), ", "), strings.Join(script.Languages(), ", ")), nil
	}

	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	if err := governance.CheckExecCommand(mcpargs.ArgString(args, "code"), root); err != nil {
		return "", err
	}

	noIsolate := mcpargs.ArgString(args, "no_isolate") == "true" || mcpargs.ArgString(args, "no_isolate") == "1"
	if noIsolate && os.Getenv("KERN_ALLOW_NO_ISOLATE") != "1" {
		noIsolate = false
	}
	run := script.Run{
		Lang:      mcpargs.ArgString(args, "lang"),
		Code:      mcpargs.ArgString(args, "code"),
		Stdin:     mcpargs.ArgString(args, "stdin"),
		NoIsolate: noIsolate,
		Egress:    mcpargs.ArgStrings(args, "egress"),
	}
	if v := mcpargs.ArgString(args, "timeout"); v != "" {
		sec, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("timeout: invalid integer %q", v)
		}
		run.Timeout = time.Duration(sec) * time.Second
	}
	if v := mcpargs.ArgString(args, "max"); v != "" {
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
		return "", fmt.Errorf("kern_exec: %s", pii.Mask(res.Err.Error()).Text)
	}
	return pii.Mask(res.Stdout).Text, nil
}
