package exec

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/validate"
)

// Validate detects and runs the project's validate/build command, returning
// the compact run_build-style report (exit status + errors), or the command
// output verbatim with raw=true.
func Validate(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
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
	var c *validate.Command
	if cmd := mcpargs.ArgString(args, "command"); cmd != "" {
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
	// Mask secrets/PII in the FULL command output before ANY truncation: a
	// secret straddling the byte cut would otherwise escape the mask regexes —
	// the mask would only ever see the truncated head and the tail of the
	// secret would pass through unmasked (same mask-before-truncate invariant
	// as maskedOutput in exec.go). The masked output feeds BOTH the raw=true
	// return and the envelope truncation.
	out := pii.Mask(res.Output).Text
	// raw=true emits the command output verbatim (the compact
	// run_build-style contract: exit status + errors, no report
	// envelope). kern_run_build was merged into this tool with this flag.
	if mcpargs.ArgBool(args, "raw") {
		if res.Err != nil {
			return out, res.Err
		}
		return out, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "command: %s %s\n", c.Cmd, strings.Join(c.Args, " "))
	fmt.Fprintf(&b, "status: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[res.OK])
	fmt.Fprintf(&b, "exit: %d\n", res.ExitCode)
	fmt.Fprintf(&b, "duration: %s\n", res.Dur.Round(time.Millisecond))
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
