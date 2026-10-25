// Package guardhook is the pure-Go PreToolUse guard hook (`kern guard hook`):
// it consumes the same stdin JSON payload and emits the same decisions as the
// legacy shell script (internal/setup/assets/hooks/kern-guard.sh) in one
// process — classify, serve, audit, allow-once and pause handling without
// sh+jq+shasum spawns. It is byte-parity with the script by construction:
// the parity battery (internal/setup/guard_hook_parity_test.go) pins the
// verdict, stderr reason, stdout shape and exit code for every arm. Hosts
// install it as the PreToolUse command; the script stays shipped as the
// fallback for setups that cannot exec the binary.
package guardhook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
	"github.com/JayveerPrajapati/kern/internal/guardpause"
	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

// codeExt is the extension set of the read arm: files outside it serve raw.
var codeExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true,
	".hh": true, ".rb": true, ".sh": true, ".bash": true, ".kt": true,
	".kts": true, ".swift": true, ".php": true, ".cs": true, ".scala": true,
	".lua": true, ".m": true, ".mm": true, ".sql": true, ".zig": true,
	".ex": true, ".exs": true, ".erl": true, ".hrl": true, ".hs": true,
	".clj": true, ".cljs": true, ".dart": true, ".vue": true, ".svelte": true,
}

func isReadTool(t string) bool {
	switch t {
	case "read", "read_file", "view_file":
		return true
	}
	return false
}

func isGrepTool(t string) bool {
	switch t {
	case "grep", "grep_file", "grep_search":
		return true
	}
	return false
}

func isGlobTool(t string) bool {
	switch t {
	case "glob", "find_by_name", "list_files":
		return true
	}
	return false
}

func isBashTool(t string) bool {
	switch t {
	case "bash", "run_shell_command", "shell", "execute_bash", "run_command":
		return true
	}
	return false
}

// env is the env-var seam for tests.
var env = os.Getenv

// projectRoot resolves the working root the hook guards.
func projectRoot() string {
	if r := env("CLAUDE_PROJECT_DIR"); r != "" {
		return r
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// kernBin resolves the serving binary: KERN_BIN → os.Executable → PATH.
func kernBin() string {
	if b := env("KERN_BIN"); b != "" {
		return b
	}
	if b, err := os.Executable(); err == nil && b != "" {
		return b
	}
	return "kern"
}

// payload holds the fields the hook reads. hasToolCall tracks Antigravity's
// {toolCall:{...}} shape, which expects JSON decisions back.
type payload struct {
	raw         string
	toolName    string
	hasToolCall bool
	toolInput   map[string]any
	toolCall    map[string]any
	input       map[string]any
}

// pick returns the first PRESENT key's string value (jq `//` semantics:
// empty string counts as present and wins over later keys).
func pick(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if m == nil {
			return ""
		}
		if _, ok := m[k]; ok {
			s, _ := m[k].(string)
			return s
		}
	}
	return ""
}

// readPath resolves the read arm's file subject.
func (p *payload) readPath() string {
	if f := pick(p.toolInput, "file_path", "path"); f != "" {
		return f
	}
	args, _ := p.toolCall["args"].(map[string]any)
	return pick(args, "filePath", "path", "AbsolutePath")
}

// grepPattern resolves the grep arm's pattern subject.
func (p *payload) grepPattern() string {
	if g := pick(p.toolInput, "pattern"); g != "" {
		return g
	}
	args, _ := p.toolCall["args"].(map[string]any)
	if g := pick(args, "pattern", "Pattern"); g != "" {
		return g
	}
	return pick(p.input, "pattern")
}

// bashCommand resolves the bash arm's command subject.
func (p *payload) bashCommand() string {
	if c := pick(p.toolInput, "command"); c != "" {
		return c
	}
	if c := pick(p.input, "command"); c != "" {
		return c
	}
	args, _ := p.toolCall["args"].(map[string]any)
	return pick(args, "command", "CommandLine")
}

func parsePayload(r io.Reader) *payload {
	data, _ := io.ReadAll(io.LimitReader(r, 8192))
	p := &payload{raw: string(data)}
	if strings.TrimSpace(p.raw) == "" {
		return p
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(p.raw), &m); err != nil {
		// Not JSON: the script's sed fallbacks still extract the tool name.
		p.toolName = sedTool(p.raw)
		p.hasToolCall = strings.Contains(p.raw, "toolCall")
		return p
	}
	p.toolInput, _ = m["tool_input"].(map[string]any)
	p.toolCall, _ = m["toolCall"].(map[string]any)
	p.input, _ = m["input"].(map[string]any)
	switch {
	case m["tool_name"] != nil:
		p.toolName, _ = m["tool_name"].(string)
	case m["tool"] != nil:
		p.toolName, _ = m["tool"].(string)
	case p.toolCall["name"] != nil:
		p.toolName, _ = p.toolCall["name"].(string)
	case m["name"] != nil:
		p.toolName, _ = m["name"].(string)
	}
	if m["toolCall"] != nil {
		// jq parity: has_tool_call is 1 only when toolCall.name exists (the
		// sed fallback's substring test agrees on every real payload).
		p.hasToolCall = p.toolCall["name"] != nil
	}
	return p
}

// sedTool mirrors the script's non-jq tool extraction: first quoted
// "tool_name" value, then "toolCall", then "name".
func sedTool(s string) string {
	for _, key := range []string{`"tool_name"`, `"toolCall"`, `"name"`} {
		i := strings.Index(s, key)
		if i >= 0 {
			if v := quotedAfter(s, i+len(key)); v != "" {
				return v
			}
		}
	}
	return ""
}

func quotedAfter(s string, from int) string {
	if from >= len(s) {
		return ""
	}
	rest := s[from:]
	i := strings.Index(rest, `"`)
	if i < 0 {
		return ""
	}
	rest = rest[i+1:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// Run executes the hook against a payload stream and returns the exit code.
func Run(stdin io.Reader, stdout, stderr io.Writer) int {
	p := parsePayload(stdin)
	tool := strings.ToLower(strings.TrimSpace(p.toolName))

	// --- bypass (env-only, audited; tty-gated confirm) ---
	if env("KERN_ENFORCE") == "0" || env("KERN_BYPASS") == "1" {
		if isReadTool(tool) || isGrepTool(tool) || isGlobTool(tool) || isBashTool(tool) {
			mode := env("KERN_BYPASS_MODE")
			if mode == "" {
				mode = "refused:no-tty"
			}
			reason := env("KERN_BYPASS_REASON")
			if reason == "" {
				reason = "unset"
			}
			if ttyConfirm() {
				mode = "confirmed"
			}
			auditBypass(tool, reason, mode)
			if mode == "confirmed" {
				fmt.Fprintln(stdout, `{"decision":"allow"}`)
				return 0
			}
		}
	}

	if tool == "" {
		if p.hasToolCall {
			fmt.Fprintln(stdout, `{"decision":"allow"}`)
		}
		return 0
	}

	// --- kern unavailable → fail open (raw serve) ---
	if kernUnavailable() {
		if p.hasToolCall {
			fmt.Fprintln(stdout, `{"decision":"allow"}`)
		}
		return 0
	}

	root := projectRoot()

	switch {
	case isReadTool(tool):
		return runReadArm(p, root, stdout, stderr)
	case isGrepTool(tool):
		return runGrepArm(p, root, stdout, stderr)
	case isGlobTool(tool):
		return runGlobArm(p, root, stdout, stderr)
	case isBashTool(tool):
		return runBashArm(p, root, stdout, stderr)
	}
	if p.hasToolCall {
		fmt.Fprintln(stdout, `{"decision":"allow"}`)
	}
	return 0
}

// runReadArm guards the built-in read: non-code files pass raw, code files
// steer to kern_explore (allow-once consumable).
func runReadArm(p *payload, root string, stdout, stderr io.Writer) int {
	if pauseActive(root) {
		return 0
	}
	fpath := p.readPath()
	if fpath != "" && !codeExt[strings.ToLower(filepath.Ext(fpath))] {
		return 0
	}
	if fpath != "" && governance.ConsumeAllowOnce(fpath) {
		auditAllowOnceConsumed(fpath)
		if p.hasToolCall {
			fmt.Fprintln(stdout, `{"decision":"allow"}`)
		}
		return 0
	}
	subjHash := governance.HashSubject(fpath)
	return deny(p, stdout, stderr, readDenyReason(subjHash), fpath)
}

// runGrepArm guards the built-in grep: steers to kern_search (symbol
// queries), allow-once consumable.
func runGrepArm(p *payload, root string, stdout, stderr io.Writer) int {
	if pauseActive(root) {
		return 0
	}
	gpat := p.grepPattern()
	if gpat != "" && governance.ConsumeAllowOnce(gpat) {
		auditAllowOnceConsumed(gpat)
		if p.hasToolCall {
			fmt.Fprintln(stdout, `{"decision":"allow"}`)
		}
		return 0
	}
	subjHash := governance.HashSubject(gpat)
	return deny(p, stdout, stderr, grepDenyReason(subjHash), gpat)
}

// runGlobArm guards the built-in glob: steers to kern_buddy.
func runGlobArm(p *payload, root string, stdout, stderr io.Writer) int {
	if pauseActive(root) {
		return 0
	}
	return deny(p, stdout, stderr, globDenyReason(), "")
}

// runBashArm is the bash arm: allow-once → classify → serve tiers →
// auto-redirect → deny with the replacement map.
func runBashArm(p *payload, root string, stdout, stderr io.Writer) int {
	cmd := p.bashCommand()
	blockedCmd := clipCmd(cmd)
	if cmd != "" && governance.ConsumeAllowOnce(cmd) {
		auditAllowOnceConsumed(cmd)
		return 0
	}
	subjHash := governance.HashSubject(cmd)
	classified := guardrule.ClassifyCommandInRoots(cmd, root, guardpaths.ExtraRootsFromEnv(env))
	if classified.Hash != "" {
		subjHash = classified.Hash
	}
	if classified.PathAware {
		auditPathWriteAllow(cmd)
	}
	if classified.Verdict == guardrule.VerdictGate {
		// Ask Protocol (P1): an active grant covers this command — a
		// single-use approved record (claimed: exactly one execution) or a
		// live prefix grant (one use consumed, counted + audit-logged in the
		// governance store). The human already answered; allow.
		if err := governance.CheckGateGrant(cmd, root); err == nil {
			auditGateGrantUse(cmd)
			return 0
		}
		return deny(p, stdout, stderr, gateDenyReason(blockedCmd, subjHash, root), cmd)
	}
	if classified.Verdict == guardrule.VerdictAllow {
		return 0
	}
	mode := env("KERN_GUARD_MODE")
	if mode != "deny" && classified.Verdict == guardrule.VerdictServe && cmd != "" {
		// The bash-steering pause is the operator's relief valve for a
		// misbehaving serve (stale index, tier bug): serve-class commands run
		// RAW instead of being redirected. GATE verdicts, exec governance and
		// the ask-protocol are never paused.
		if _, paused, perr := guardpause.ActiveScoped(root, guardpause.ScopeBashSteering); perr == nil && paused {
			return 0
		}
		if code, ok := servePipeline(cmd, classified, root, stdout); ok {
			return code
		}
	}
	if cmd != "" && mode != "deny" {
		if code, ok := autoRedirect(cmd, stdout); ok {
			return code
		}
	}
	// The script's fallthrough deny (serve refused, redirect missed, empty
	// command) is the GOVERNED reason — the bash arm never reuses the GATE
	// text once the gate verdict check above has not fired.
	return deny(p, stdout, stderr, governedDenyReason(blockedCmd, subjHash, replacementFor(cmd)), cmd)
}

// deny emits the deny decision: stderr reason (always), antigravity JSON
// (when toolCall-shaped), audit record, exit 2.
func deny(p *payload, stdout, stderr io.Writer, reason, subject string) int {
	if env("KERN_ENFORCE") != "0" && env("KERN_BYPASS") != "1" {
		auditDeny(strings.ToLower(p.toolName), subject)
	}
	if p.hasToolCall {
		fmt.Fprintf(stdout, "{\"decision\":\"deny\",\"reason\":\"%s\"}\n", reason)
	}
	fmt.Fprintln(stderr, reason)
	return 2
}

// clipCmd truncates a command to 80 chars with an ellipsis.
func clipCmd(cmd string) string {
	if len(cmd) <= 80 {
		return cmd
	}
	return cmd[:80] + "…"
}

// ttyConfirm mirrors the script's break-glass probe: opening /dev/tty
// succeeds only for a human at an interactive terminal.
func ttyConfirm() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// timestamp renders the script's date -u +%Y-%m-%dT%H:%M:%SZ shape.
func timestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// kernUnavailable reports the fail-open conditions: KERN_DISABLED, an
// explicit .kern/off, or a mid-flight index build (marker files in
// <root>/.kern/indexing touched within 15 minutes). KERN_GUARD_ASSUME_KERN
// forces availability so the governed paths stay testable.
func kernUnavailable() bool {
	if env("KERN_GUARD_ASSUME_KERN") == "1" {
		return false
	}
	if env("KERN_DISABLED") == "1" {
		return true
	}
	root := projectRoot()
	if _, err := os.Stat(filepath.Join(root, ".kern", "off")); err == nil {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(root, ".kern", "indexing"))
	if err != nil {
		return false
	}
	cutoff := time.Now().Add(-15 * time.Minute)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

// pauseActive consults the pause registry (read/grep/glob steering only).
func pauseActive(root string) bool {
	_, ok, err := guardpause.Active(root)
	return err == nil && ok
}
