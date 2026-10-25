// serve.go implements the guard's serving tiers and the governed
// auto-redirect map, byte-parity with the script's flow: tier 1 answers from
// the index (buddy/ast/compact), tier 2 adds a tail computation, tier 3 runs
// the pure-read pipeline in the readonly sandbox; the auto-redirect map
// answers common single-token commands via kern CLI equivalents.
package guardhook

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

// runKern executes the serving binary with args and returns its combined
// output and success.
func runKern(args ...string) (string, bool) {
	out, err := exec.Command(kernBin(), args...).CombinedOutput()
	return string(out), err == nil
}

// runKernDir executes the serving binary with a working directory.
func runKernDir(dir string, args ...string) (string, int) {
	cmd := exec.Command(kernBin(), args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

// emitShaped prints a served output the way the script does: command
// substitution strips ALL trailing newlines, then printf '%s\n' adds exactly
// one back.
func emitShaped(stdout io.Writer, out string) {
	fmt.Fprintf(stdout, "%s\n", strings.TrimRight(out, "\n"))
}

// servePipeline runs the serve tiers for a classified pipeline. A true
// return means the pipeline was served (code is the hook exit code); false
// falls through to auto-redirect/deny.
func servePipeline(cmd string, res guardrule.ClassifyResult, root string, stdout io.Writer) (int, bool) {
	switch res.Tier {
	case 1:
		switch res.Head {
		case "find":
			if out, ok := runKern("buddy"); ok {
				auditRedirect("find", "buddy", "")
				emitShaped(stdout, out)
				return 0, true
			}
		case "grep", "egrep", "rg", "ag":
			if res.Pattern != "" {
				if out, ok := runKern("ast", res.Pattern); ok {
					auditRedirect(res.Head, "ast "+res.Pattern, res.Pattern)
					emitShaped(stdout, out)
					return 0, true
				}
			}
		case "cat", "head", "tail", "less", "more":
			if res.File != "" {
				if out, ok := runKern("compact", res.File); ok {
					auditRedirect(res.Head, "compact "+res.File, res.File)
					emitShaped(stdout, out)
					return 0, true
				}
			}
		}
		return 0, false
	case 2:
		if res.Pattern != "" {
			if headout, ok := runKern("ast", res.Pattern); ok {
				result := applyTails(cmd, headout)
				auditRedirect(res.Head, "ast "+res.Pattern+" | tail", res.Pattern)
				emitShaped(stdout, result)
				return 0, true
			}
		}
		return 0, false
	case 3:
		script := "cd '" + root + "' && " + cmd
		body, code := runKernDir(root, "exec", "--readonly", "--lang", "sh", script)
		if code == 0 || code == 1 {
			auditRedirect(res.Head, "exec --readonly", "")
			emitShaped(stdout, body)
			return 0, true
		}
		if strings.Contains(body, "denied by governance") ||
			strings.Contains(body, "blocked by governance") ||
			strings.Contains(body, "approval required") {
			return 0, false
		}
		// Fail-open raw serve: allow the original pipeline through.
		fmt.Fprintf(stdout, "%s\n", cmd)
		fmt.Fprintln(stdout, "[kern] index unavailable/stale — raw read served (pure-read pipeline)")
		auditRedirect(res.Head, "exec --readonly (fallback raw)", "")
		return 0, true
	}
	return 0, false
}

// applyTails ports the script's tier-2 tail computation: everything after
// the last pipe decides the post-processing (wc -l / head / tail / grep /
// grep -v), applied to the head command's output.
func applyTails(cmd, headout string) string {
	i := strings.LastIndex(cmd, "|")
	if i < 0 {
		return headout
	}
	tailpart := strings.TrimSpace(cmd[i+1:])
	fields := strings.Fields(tailpart)
	if len(fields) == 0 {
		return headout
	}
	switch {
	case tailIsWC(tailpart):
		count := 0
		for _, l := range strings.Split(headout, "\n") {
			if l != "" {
				count++
			}
		}
		return strconv.Itoa(count) + "\n"
	case fields[0] == "head":
		return headLines(headout, firstNumber(fields, 10))
	case fields[0] == "tail":
		return tailLines(headout, firstNumber(fields, 10))
	case fields[0] == "grep" && len(fields) > 1 && fields[1] == "-v":
		return filterLines(headout, lastField(fields), false)
	case fields[0] == "grep":
		return filterLines(headout, lastField(fields), true)
	}
	return headout
}

// tailIsWC matches the script's `wc *-l*` / `wc -l*` shapes.
func tailIsWC(tailpart string) bool {
	fields := strings.Fields(tailpart)
	if len(fields) == 0 || fields[0] != "wc" {
		return false
	}
	for _, f := range fields[1:] {
		if strings.Contains(f, "l") && strings.HasPrefix(f, "-") {
			return true
		}
	}
	return false
}

func firstNumber(fields []string, def int) int {
	for _, f := range fields[1:] {
		if n, err := strconv.Atoi(f); err == nil {
			return n
		}
	}
	return def
}

func lastField(fields []string) string {
	return fields[len(fields)-1]
}

func headLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n > len(lines) {
		n = len(lines)
	}
	return strings.Join(lines[:n], "\n")
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n > len(lines) {
		n = len(lines)
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func filterLines(s, needle string, keep bool) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) == keep {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// autoRedirect ports the script's governed-command redirect map: simple
// single-token commands answered directly via the kern CLI. A true return
// means the command was answered (code is the hook exit code).
func autoRedirect(cmd string, stdout io.Writer) (int, bool) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return 0, false
	}
	// Compound / quoted / substituted commands are never auto-redirected.
	for _, r := range cmd {
		switch r {
		case '\n', '\r', '|', '&', ';', '<', '>', '`', '$', '"', '\'', '\\':
			return 0, false
		}
	}
	first := fields[0]
	nonFlags := make([]string, 0, len(fields)-1)
	for _, f := range fields[1:] {
		if !strings.HasPrefix(f, "-") {
			nonFlags = append(nonFlags, f)
		}
	}
	switch first {
	case "grep", "rg", "ag":
		if len(nonFlags) > 0 {
			subj := nonFlags[0]
			if out, ok := runKern("ast", subj); ok {
				auditRedirect(first, "ast "+subj, subj)
				emitShaped(stdout, out)
				return 0, true
			}
		}
	case "cat", "head", "tail", "sed", "awk", "less", "more":
		if len(nonFlags) > 0 {
			subj := nonFlags[len(nonFlags)-1]
			if out, ok := runKern("compact", subj); ok {
				auditRedirect(first, "compact "+subj, subj)
				emitShaped(stdout, out)
				return 0, true
			}
		}
	case "find":
		if out, ok := runKern("buddy"); ok {
			auditRedirect(first, "buddy", "")
			emitShaped(stdout, out)
			return 0, true
		}
	case "go", "make":
		if out, ok := runKern("verify", "--command", cmd, "--output", "summary"); ok {
			auditRedirect(first, "verify --command "+cmd+" --output summary", cmd)
			emitShaped(stdout, out)
			return 0, true
		}
	}
	return 0, false
}
