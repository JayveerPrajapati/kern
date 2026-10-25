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

	"github.com/JayveerPrajapati/kern/internal/execution"
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

// Sandbox runs a command in a snapshot rollback worktree. This is not
// OS-level privilege isolation: the command still runs with the user’s
// privileges, and out-of-root writes are not guaranteed to roll back.
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
		sec, err := parseTimeoutSecs(s)
		if err != nil {
			return "", err
		}
		if sec > 0 {
			timeout = scriptTimeout(sec)
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

// maxExecScriptTimeout is the hard ceiling on a single kern_exec script's
// runtime. The MCP server's per-call deadline is 30 minutes
// (toolCallResponseDecoded), so a timeout beyond that could never be honored
// by the enclosing context anyway; clamping keeps an oversized caller-supplied
// value (e.g. timeout=999999999) from silently meaning "effectively forever"
// on direct (non-server) paths. The local operator can raise the ceiling
// per-invocation with KERN_EXEC_MAX_TIMEOUT_SEC (a positive integer; invalid
// or unset values keep this cap).
const maxExecScriptTimeout = 30 * time.Minute

func scriptTimeout(sec int) time.Duration {
	d := time.Duration(sec) * time.Second
	if cap := execScriptTimeoutCap(); d > cap {
		d = cap
	}
	if d <= 0 {
		return 10 * time.Second
	}
	return d
}

// parseTimeoutSecs parses the shared `timeout` argument (seconds). Negative
// values are rejected loudly (NIT-19b: they were silently ignored, running
// with the default); zero means "keep the default" and is accepted to stay
// compatible with clients that send an explicit 0.
func parseTimeoutSecs(s string) (int, error) {
	sec, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("timeout: invalid integer %q", s)
	}
	if sec < 0 {
		return 0, fmt.Errorf("timeout: negative integer %d (want a positive number of seconds)", sec)
	}
	return sec, nil
}

func execScriptTimeoutCap() time.Duration {
	if ov := strings.TrimSpace(os.Getenv("KERN_EXEC_MAX_TIMEOUT_SEC")); ov != "" {
		if c, err := strconv.Atoi(ov); err == nil && c > 0 {
			return time.Duration(c) * time.Second
		}
	}
	return maxExecScriptTimeout
}

// maxExecMaxOut is the hard ceiling (4 MiB) on a single script's retained
// stdout. A larger --max value is clamped so the in-memory anchor store and
// the exec-runs disk mirror stay bounded; the local operator can raise the
// ceiling per-invocation with KERN_EXEC_MAX_BYTES.
const maxExecMaxOut = 4 << 20

// defaultExecTimeout is the execution timeout for RunBuild when
// KERN_EXEC_TIMEOUT is unset or invalid.
const defaultExecTimeout = 5 * time.Minute

// execTimeout returns the execution timeout for RunBuild: the KERN_EXEC_TIMEOUT
// env var parsed as a Go duration (e.g. "90s", "10m") when set and valid,
// otherwise the 5-minute default. Invalid or non-positive values fall back to
// the default with a warning line so a typo never silently changes the
// timeout.
func execTimeout() time.Duration {
	v := os.Getenv("KERN_EXEC_TIMEOUT")
	if v == "" {
		return defaultExecTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		fmt.Fprintf(os.Stderr, "kern_exec: warning: invalid KERN_EXEC_TIMEOUT %q (want a positive Go duration like \"90s\" or \"10m\"); using default %s\n", v, defaultExecTimeout)
		return defaultExecTimeout
	}
	return d
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
	bctx, cancel := context.WithTimeout(ctx, execTimeout())
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
	// Output shaping + kept-run re-slice (the kern_verify contract):
	// anchor=<id> re-slices a previously KEPT run with a different
	// output= mode WITHOUT re-executing anything — it must therefore sit
	// BEFORE the governance gate (no code runs on this path) and before
	// the code-required check. output= summary|tail:N|lines:A-B|full
	// (default full: kern_exec's contract is exact answers, so shaping is
	// opt-in).
	if id := mcpargs.ArgString(args, "anchor"); id != "" {
		full, err := execution.LoadExecRun(root, id)
		if err != nil {
			return "", err
		}
		return execution.ShapeExecOutput(mcpargs.ArgString(args, "output"), full, execution.ExecMeta{Anchor: id})
	}
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
		Ctx:       ctx,
	}
	if v := mcpargs.ArgString(args, "timeout"); v != "" {
		sec, err := parseTimeoutSecs(v)
		if err != nil {
			return "", err
		}
		run.Timeout = scriptTimeout(sec)
	}
	if v := mcpargs.ArgString(args, "max"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("max: invalid integer %q", v)
		}
		if n > 0 {
			// Hard cap: retained stdout is bounded so a runaway script
			// cannot balloon the in-memory anchor store or the exec-runs
			// disk mirror without limit. Anything above the cap is clamped;
			// the local operator can raise the ceiling per-invocation via
			// KERN_EXEC_MAX_BYTES (a positive integer; invalid or unset
			// values keep the default cap).
			cap := maxExecMaxOut
			if ov := strings.TrimSpace(os.Getenv("KERN_EXEC_MAX_BYTES")); ov != "" {
				if c, err := strconv.Atoi(ov); err == nil && c > 0 {
					cap = c
				}
			}
			if n > cap {
				n = cap
			}
			run.MaxOut = n
		}
	}
	res := script.RunScript(run)
	if res.Err != nil {
		return "", fmt.Errorf("kern_exec: %s", pii.Mask(res.Err.Error()).Text)
	}
	masked := pii.Mask(res.Stdout)
	// Keep the full (masked) output: in-memory anchor + on-disk copy, so a
	// shaped call can always be widened (output=full) or re-windowed later
	// without re-running the script.
	anchor := optimize.StoreAnchor(masked.Text)
	execution.SaveExecRun(root, anchor, masked.Text)
	out, err := execution.ShapeExecOutput(mcpargs.ArgString(args, "output"), masked.Text, execution.ExecMeta{
		Runtime: res.Runtime,
		DurMS:   res.Duration.Milliseconds(),
		Exit:    res.ExitCode,
		Anchor:  anchor,
	})
	if err != nil {
		return "", err
	}
	// Post-hoc steering: a script that statically reads files AND returned
	// a large raw stdout is a dump through the wrong door (the token-cheap
	// surfaces are kern_explore/kern_search). Advisory only — appended,
	// never blocking, never mutating the payload above it.
	if footer := execution.DumpSteer(run.Code, masked.Text, anchor); footer != "" {
		out += "\n" + footer + "\n"
	}
	return out, nil
}
