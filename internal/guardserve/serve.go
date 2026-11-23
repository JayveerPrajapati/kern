// Package guardserve implements the guard's serving tiers and the governed
// auto-redirect map, byte-parity with the script's flow: tier 1 answers from
// the index (buddy/ast/compact), tier 2 adds a tail computation, tier 3 runs
// the pure-read pipeline in the readonly sandbox; the auto-redirect map
// answers common single-token commands via kern CLI equivalents.
//
// It was extracted from internal/guardhook (the headroom policy's candidate)
// so the hook package keeps only payload/dispatch, audit and reasons. The two
// host couplings are injected: the kern binary resolver and the redirect audit
// sink — guardserve never imports guardhook.
package guardserve

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

// Server serves classified pipelines and auto-redirected commands on behalf of
// the guard hook. kernBin resolves the serving binary; audit records a
// redirect (nil is tolerated: a host without an audit chain still serves).
type Server struct {
	kernBin func() string
	audit   func(tool, kernCmd, subject string)
}

// New builds a Server over the host's binary resolver and audit sink.
func New(kernBin func() string, audit func(tool, kernCmd, subject string)) *Server {
	return &Server{kernBin: kernBin, audit: audit}
}

func (s *Server) auditRedirect(tool, kernCmd, subject string) {
	if s.audit != nil {
		s.audit(tool, kernCmd, subject)
	}
}

// runKern executes the serving binary with args and returns its combined
// output and success.
func (s *Server) runKern(args ...string) (string, bool) {
	out, err := exec.Command(s.kernBin(), args...).CombinedOutput()
	return string(out), err == nil
}

// runKernStdout executes the serving binary keeping stdout and stderr apart — a
// stdout-only answer (compact's empty "not indexable" signal) must not see stderr footers.
func (s *Server) runKernStdout(dir string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	cmd := exec.Command(s.kernBin(), args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return out.String(), errb.String(), code
}

// runKernDir executes the serving binary with a working directory.
func (s *Server) runKernDir(dir string, args ...string) (string, int) {
	return s.runKernDirEnv(dir, nil, args...)
}

// runKernDirEnv is runKernDir with extra environment entries.
func (s *Server) runKernDirEnv(dir string, extraEnv []string, args ...string) (string, int) {
	cmd := exec.Command(s.kernBin(), args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
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

// Pipeline runs the serve tiers for a classified pipeline. A true
// return means the pipeline was served (code is the hook exit code); false
// falls through to auto-redirect/deny.
func (s *Server) Pipeline(cmd string, res guardrule.ClassifyResult, root string, stdout io.Writer, hasToolCall bool) (int, bool) {
	switch res.Tier {
	case 1:
		switch res.Head {
		case "find":
			if out, ok := s.runKern("buddy"); ok {
				s.auditRedirect("find", "buddy", "")
				emitShaped(stdout, out)
				return 0, true
			}
		case "grep", "egrep", "rg", "ag":
			if res.Pattern != "" {
				// A scoped grep must stay scoped: the repo-wide symbol answer
				// would be a fidelity regression (parity with the script).
				var out string
				var ok bool
				if res.Scope != "" {
					var code int
					out, code = s.runKernDir(root, "search", res.Pattern, "--path", res.Scope)
					ok = code == 0
					if ok {
						s.auditRedirect(res.Head, "search "+res.Pattern+" --path "+res.Scope, res.Pattern)
					}
				} else {
					out, ok = s.runKern("ast", res.Pattern)
					if ok {
						s.auditRedirect(res.Head, "ast "+res.Pattern, res.Pattern)
					}
				}
				if ok {
					emitShaped(stdout, out)
					return 0, true
				}
			}
		case "cat":
			if res.File != "" {
				// --root root: resolve the token against the hook's working
				// root (the agent's CWD) — not kern's project-root default,
				// which turns a legitimate subdirectory read into a miss.
				// --machine + stdout-only: an empty answer means the file is
				// not indexable, which must fall through to the raw Tier-3
				// read below (never serve a partial/stub answer).
				out, _, code := s.runKernStdout(root, nil, "compact", "--machine", "--root", root, res.File)
				if code == 0 && strings.TrimSpace(out) != "" {
					s.auditRedirect(res.Head, "compact "+res.File, res.File)
					emitShaped(stdout, out)
					return 0, true
				}
			}
		}
		// Index-backed serve unavailable (fresh project / stale index): fall
		// back to the readonly sandbox instead of denying a pure read.
		return s.serveTier3(cmd, res, root, stdout, hasToolCall)
	case 2:
		if res.Pattern != "" {
			var headout string
			var ok bool
			if res.Scope != "" {
				var code int
				headout, code = s.runKernDir(root, "search", res.Pattern, "--path", res.Scope)
				ok = code == 0
			} else {
				headout, ok = s.runKern("ast", res.Pattern)
			}
			if ok {
				result := applyTails(cmd, headout)
				s.auditRedirect(res.Head, "ast/search "+res.Pattern+" | tail", res.Pattern)
				emitShaped(stdout, result)
				return 0, true
			}
		}
		return s.serveTier3(cmd, res, root, stdout, hasToolCall)
	case 3:
		return s.serveTier3(cmd, res, root, stdout, hasToolCall)
	}
	return 0, false
}

// serveTier3 executes a pure read in the readonly sandbox. code 0/1 is a
// served answer; a governance denial falls through to the deny; any other
// failure fails open (the host runs the read raw).
func (s *Server) serveTier3(cmd string, res guardrule.ClassifyResult, root string, stdout io.Writer, hasToolCall bool) (int, bool) {
	script := "cd '" + root + "' && " + cmd
	// The sandbox hides .git/.kern, so a daemon started from in there would
	// key its socket on the subdirectory and outlive the read.
	body, code := s.runKernDirEnv(root, []string{"KERN_DAEMON=0"}, "exec", "--readonly", "--quiet", "--lang", "sh", script)
	if code == 0 || code == 1 {
		s.auditRedirect(res.Head, "exec --readonly", "")
		emitShaped(stdout, body)
		return 0, true
	}
	if strings.Contains(body, "denied by governance") ||
		strings.Contains(body, "blocked by governance") ||
		strings.Contains(body, "approval required") {
		return 0, false
	}
	if hasToolCall {
		fmt.Fprintln(stdout, `{"decision":"allow"}`)
	}
	fmt.Fprintln(os.Stderr, "[kern] index unavailable/stale — raw read served (pure-read pipeline)")
	s.auditRedirect(res.Head, "exec --readonly (fallback raw)", "")
	return 0, true
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

// AutoRedirect ports the script's governed-command redirect map: simple
// single-token commands answered directly via the kern CLI. A true return
// means the command was answered (code is the hook exit code).
func (s *Server) AutoRedirect(cmd string, stdout io.Writer) (int, bool) {
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
			if out, ok := s.runKern("ast", subj); ok {
				s.auditRedirect(first, "ast "+subj, subj)
				emitShaped(stdout, out)
				return 0, true
			}
		}
	case "cat", "head", "tail", "sed", "awk", "less", "more":
		if len(nonFlags) > 0 {
			subj := nonFlags[len(nonFlags)-1]
			if out, ok := s.runKern("compact", subj); ok {
				s.auditRedirect(first, "compact "+subj, subj)
				emitShaped(stdout, out)
				return 0, true
			}
		}
	case "find":
		if out, ok := s.runKern("buddy"); ok {
			s.auditRedirect(first, "buddy", "")
			emitShaped(stdout, out)
			return 0, true
		}
	case "go", "make":
		if out, ok := s.runKern("verify", "--command", cmd, "--output", "summary"); ok {
			s.auditRedirect(first, "verify --command "+cmd+" --output summary", cmd)
			emitShaped(stdout, out)
			return 0, true
		}
	}
	return 0, false
}
