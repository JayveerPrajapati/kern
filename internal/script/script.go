// Package script runs code in an isolated local runtime and returns only
// stdout — the "Think in Code" surface of a local context optimizer. An agent
// can compute things (data munging, math, JSON transforms, quick sims) without
// polluting its context with build noise or stderr.
// Isolation model: the script runs in a fresh temp dir with the runtime
// resolved from PATH, a hard timeout, a stdout byte cap, and a sanitized
// environment (HOME and the XDG dirs point into the temp dir, so a script
// cannot read or clobber the user's real configs or env secrets). When the
// system's unprivileged user namespaces are enabled, the child also runs in a
// private network namespace (unshare --user --map-root-user --net), so network
// egress is blocked. Network isolation fails closed on every platform: if
// user/network namespaces are unavailable the run is refused rather than
// silently degrading to full network egress, unless the local operator
// explicitly opts in via KERN_ALLOW_UNISOLATED=1 (or the pre-existing alias
// KERN_ALLOW_NET=1). Stderr is never mixed into stdout — it is only surfaced
// on failure.
package script

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/processgroup"
)

// runtime describes how to execute one language.
type runtime struct {
	bin  string   // binary to look up on PATH
	ext  string   // file extension for the temp source file
	pre  []string // arguments before the source path (e.g. "run" for go)
	post []string // two-step runtimes: extra args to the binary itself (rustc flags)
}

// runtimes maps a language name to its runtime. Only languages whose binary is
// actually installed are runnable; Available() reports the subset present.
var runtimes = map[string]runtime{
	"python3": {bin: "python3", ext: ".py"},
	"python":  {bin: "python", ext: ".py"},
	"node":    {bin: "node", ext: ".js"},
	"bun":     {bin: "bun", ext: ".ts"},
	"deno":    {bin: "deno", ext: ".ts"},
	"bash":    {bin: "bash", ext: ".sh"},
	"sh":      {bin: "sh", ext: ".sh"},
	"perl":    {bin: "perl", ext: ".pl"},
	"ruby":    {bin: "ruby", ext: ".rb"},
	"php":     {bin: "php", ext: ".php"},
	"lua":     {bin: "lua", ext: ".lua"},
	"julia":   {bin: "julia", ext: ".jl"},
	"R":       {bin: "Rscript", ext: ".R"},
	"go":      {bin: "go", ext: ".go", pre: []string{"run"}},
	"rust":    {bin: "rustc", ext: ".rs", post: []string{"-o", "prog", "--edition", "2021"}},
}

// Available returns the installed runtime names, sorted, with their binaries.
func Available() []string {
	var out []string
	for name := range runtimes {
		if _, err := exec.LookPath(runtimes[name].bin); err == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Run is a script execution request.
type Run struct {
	Lang    string        // explicit language; detected from shebang/extension when empty
	Code    string        // script body (required unless Path is set)
	Path    string        // optional source file to run instead of Code
	Stdin   string        // piped to the script's stdin
	Timeout time.Duration // default 10s
	MaxOut  int           // max stdout bytes returned; default 16 KiB
	// NoIsolate opts out of the sandbox: when set, the script inherits the
	// caller's environment and full network access. Isolation is on by default,
	// and NoIsolate is only honored when the local operator has explicitly
	// opted in via KERN_ALLOW_NO_ISOLATE=1 — otherwise it is silently ignored
	// and isolation is kept (an arbitrary agent call can never drop isolation).
	NoIsolate bool
	// Egress lists operator/caller-supplied egress targets ("host:port") this
	// script may contact. They are semantically identical to `# egress:` /
	// `// egress:` comment declarations: on unisolated runs the deny-by-default
	// gate is satisfied by structured OR comment declarations (union — both
	// apply), every structured target is policy-checked via CheckEgressResource,
	// and an entry that is not a valid "host:port" refuses the run with an
	// error naming the offending value. Optional; callers that don't set it are
	// unaffected.
	Egress []string
}

// Result is the outcome of one script execution.
type Result struct {
	OK        bool          `json:"ok"`
	ExitCode  int           `json:"exit_code"`
	Lang      string        `json:"lang"`
	Runtime   string        `json:"runtime"`
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr,omitempty"`
	Truncated bool          `json:"truncated"`
	TimedOut  bool          `json:"timed_out"`
	Isolated  bool          `json:"isolated"` // network ns active (false = degraded)
	Duration  time.Duration `json:"duration"`
	Err       error         `json:"-"`
}

// sensitivePathDirs mirrors internal/sandbox's macOS Stage 1 blocklist: the
// operator-private locations a script must never be able to read through an
// ABSOLUTE path. The HOME redirect in sandboxEnv already blocks relative
// lookups (~/.ssh resolves into the temp dir); these Seatbelt file-read-data
// subpath denies close the gap for absolute ones (~/.ssh/id_rsa,
// ~/.aws/credentials, ...). Keep in sync with internal/sandbox/network.go.
var sensitivePathDirs = []string{
	".ssh",
	".aws",
	".gnupg",
	".config/gcloud",
	".kube",
	".docker/config.json",
	"Library/Cookies",
	".netrc",
}

// fsConfinementEnabled reports whether script runs add the sensitive-path
// read blocklist to the Seatbelt profile (KERN_SANDBOX_FS_CONFINEMENT,
// default ON; "0"/"false"/"off" disables — the codebase's "0 disables"
// convention). Mirrors internal/sandbox's gate; the KERN_ALLOW_UNISOLATED /
// KERN_ALLOW_NET escape hatch covers this surface too (it skips the
// seatbelt wrap entirely).
func fsConfinementEnabled() bool {
	switch strings.TrimSpace(os.Getenv("KERN_SANDBOX_FS_CONFINEMENT")) {
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	}
	return true
}

// seatbeltScriptProfile builds the Apple Seatbelt profile that denies network
// egress AND — when filesystem read confinement is enabled — read access to
// the operator's sensitive-path blocklist. BLOCKLIST-deny: (allow default)
// is preserved, so nothing outside these paths is affected. Network posture
// is unchanged from the pre-confinement profile (no loopback allowance).
func seatbeltScriptProfile() string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny network*)")
	if !fsConfinementEnabled() {
		return b.String()
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return b.String()
	}
	// The as-written $HOME is used as-is: each deny below covers BOTH the
	// as-written and the canonical spelling, so canonicalizing here would
	// throw away the alias the kernel may match on (R2 root cause).
	for _, p := range sensitivePathDirs {
		joined := filepath.Join(home, p)
		// Deny BOTH spellings (mirrors internal/sandbox R2 fix): the darwin
		// kernel canonicalizes the ACCESSED path but matches profile subpaths
		// as written, so a symlinked home (e.g. /var -> /private/var) can be
		// accessed under either alias depending on name-cache state. The
		// as-written join is always emitted; the canonical form is added when
		// it resolves and differs (fail-safe: single deny on resolution
		// error). Denies are purely additive under the blocklist design.
		fmt.Fprintf(&b, "\n(deny file-read-data (subpath %q))", joined)
		if canon, err := filepath.EvalSymlinks(joined); err == nil && canon != joined {
			fmt.Fprintf(&b, "\n(deny file-read-data (subpath %q))", canon)
		}
	}
	return b.String()
}

// networkNSBin caches the isolation wrapper probe: whether this host can run
// a script with network egress blocked is a per-host fact that does not
// change during a process lifetime. The profile string itself is NOT cached
// — it is regenerated per call so the sensitive-path blocklist always
// reflects the current KERN_SANDBOX_FS_CONFINEMENT / HOME (same design as
// internal/sandbox's netIsolationPrefix).
var (
	netProbeOnce sync.Once
	netNSBin     string
)

// networkNS returns the isolation prefix that runs a child with network egress
// blocked (Linux unshare network namespace or macOS sandbox-exec Seatbelt),
// or nil when unavailable (probed once per process). On darwin the profile
// also carries the sensitive-path read blocklist (Stage 1 FS confinement)
// when KERN_SANDBOX_FS_CONFINEMENT is on (the default).
func networkNS() []string {
	netProbeOnce.Do(func() {
		if goruntime.GOOS == "darwin" {
			if bin, err := exec.LookPath("sandbox-exec"); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				// The probe validates the EXACT generated profile the wrap
				// uses (seatbeltScriptProfile, incl. the sensitive-path
				// blocklist under the real home when confinement is on):
				// availability and enforcement are the same mechanism.
				if err := exec.CommandContext(ctx, bin, "-p", seatbeltScriptProfile(), "true").Run(); err == nil {
					netNSBin = bin
					return
				}
			}
		}
		bin, err := exec.LookPath("unshare")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, bin, "--user", "--map-root-user", "--net", "true").Run(); err == nil {
			netNSBin = bin
		}
	})
	if netNSBin == "" {
		return nil
	}
	if goruntime.GOOS == "darwin" {
		return []string{netNSBin, "-p", seatbeltScriptProfile()}
	}
	return []string{netNSBin, "--user", "--map-root-user", "--net"}
}

// allowUnisolated reports whether the local operator has explicitly opted in
// to running scripts without network isolation (KERN_ALLOW_UNISOLATED, or the
// pre-existing alias KERN_ALLOW_NET). Only the operator's environment can set
// this — an arbitrary agent call never can. Parsing matches the sandbox
// escape-hatch acceptance (netEscapeHatchSet in internal/sandbox/network.go):
// "1", "true", "TRUE" and "True" all opt in; any other value ("0", "no", ...)
// is treated as unset.
func allowUnisolated() bool {
	on := func(name string) bool {
		switch strings.TrimSpace(os.Getenv(name)) {
		case "1", "true", "TRUE", "True":
			return true
		}
		return false
	}
	return on("KERN_ALLOW_UNISOLATED") || on("KERN_ALLOW_NET")
}

// parseEgressTargets scans script source for egress declaration lines —
// "# egress:" (shell/Python-family) and "// egress:" (JS/TS-family) comments,
// exact case-sensitive prefix, any leading whitespace allowed — and returns
// the trimmed non-empty target values in order of appearance.
func parseEgressTargets(code string) []string {
	var targets []string
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		var target string
		switch {
		case strings.HasPrefix(trimmed, "# egress:"):
			target = strings.TrimSpace(strings.TrimPrefix(trimmed, "# egress:"))
		case strings.HasPrefix(trimmed, "// egress:"):
			target = strings.TrimSpace(strings.TrimPrefix(trimmed, "// egress:"))
		}
		if target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

// networkCallRe matches network-shaped operations across languages. It is a
// deliberately conservative heuristic (deny-by-default): a match does not
// prove the script reaches the network, but the heuristic must not let real
// egress slip through the unisolated gate. Bare command names (curl, wget,
// ssh, ...) are matched on word boundaries; API forms (http.Get, net.Dial,
// fetch, requests., urllib, ...) match their dotted/parameter shapes.
var networkCallRe = regexp.MustCompile(
	`(?i)\b(curl|wget|nc|netcat|ssh|scp|socket|fetch|axios|node-fetch|httpx|aiohttp|urllib|HttpClient|Invoke-WebRequest|Invoke-RestMethod)\b` +
		`|net/http|net\.Dial|http\.(Get|Post|NewRequest|Client)|requests\.`)

// containsNetworkCall reports whether code contains a network-shaped
// operation. Used by the egress gate to deny-by-default: an unisolated script
// that performs network operations must declare its egress targets.
func containsNetworkCall(code string) bool {
	return networkCallRe.MatchString(code)
}

// egressGate enforces the egress policy on unisolated runs. When the run is
// network-isolated the netns blocks egress anyway, so no check is needed.
// Unisolated runs are deny-by-default: a script whose code performs
// network-shaped operations must declare at least one egress target —
// either a structured Run.Egress entry or a "# egress:" (or "// egress:")
// comment — otherwise the run is refused. Structured and comment
// declarations are merged (union: both apply) and every declared target is
// checked against the operator's KERN_EGRESS_POLICY (default local-only —
// fail closed); any denied target refuses the run. A structured entry that
// is not a valid "host:port" is refused with an error naming the offending
// value (comment declarations keep their existing lenient handling).
func egressGate(code string, structured []string, isolated bool) error {
	if isolated {
		return nil
	}
	var targets []string
	for _, target := range structured {
		t := strings.TrimSpace(target)
		if t == "" {
			continue
		}
		if !validEgressTarget(t) {
			return fmt.Errorf("invalid egress target %q: expected \"host:port\" (e.g. \"api.example.com:443\"); fix or remove the egress declaration", t)
		}
		targets = append(targets, t)
	}
	targets = append(targets, parseEgressTargets(code)...)
	if len(targets) == 0 && containsNetworkCall(code) {
		return errors.New("script performs network operations but declares no egress targets; add `# egress: host:port` (or `// egress:`) declarations, pass the egress argument, or run isolated")
	}
	rule := governance.EgressRule{Policy: governance.EgressPolicyFromEnv()}
	for _, target := range targets {
		if dec := governance.CheckEgressResource(rule, target); !dec.Allowed {
			return fmt.Errorf("egress policy %q denies target %q (%s; fail-closed); set KERN_EGRESS_POLICY=external-redacted (or external-approved) to allow", rule.Policy, target, dec.Reason)
		}
	}
	return nil
}

// validEgressTarget reports whether target is a well-formed "host:port"
// egress declaration: a non-empty host and a numeric port in 1..65535
// (IPv6 addresses bracketed as usual for net.SplitHostPort). Empty and
// missing-port values fail so callers get a clear error naming the offending
// structured declaration instead of a generic policy denial.
func validEgressTarget(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// NetworkIsolationAvailable reports whether script runs can be network-isolated
// on this host (unprivileged user + network namespaces). kern doctor uses it to
// report the platform's isolation capability honestly.
func NetworkIsolationAvailable() bool {
	return networkNS() != nil
}

// sandboxEnv builds a minimal environment with HOME and the XDG dirs pointed
// into the sandbox dir, so a script cannot read the user's real configs or
// exfiltrate environment secrets. Whitelisted vars that are safe and useful
// are preserved.
func sandboxEnv(dir string) []string {
	env := []string{
		"HOME=" + dir,
		"XDG_CACHE_HOME=" + filepath.Join(dir, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(dir, ".local/share"),
		"TMPDIR=" + dir,
		"TMP=" + dir,
		"TEMP=" + dir,
		"PATH=" + os.Getenv("PATH"),
	}
	for _, k := range []string{"LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ", "KERN_EMBED_MODEL"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// RunScript executes the script and returns a Result. On success Stdout holds
// the (possibly truncated) script output and Stderr is empty; on failure
// Stderr carries the truncated error output.
func RunScript(r Run) *Result {
	res := &Result{Lang: r.Lang}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	if r.Timeout <= 0 {
		r.Timeout = 10 * time.Second
	}
	if r.MaxOut <= 0 {
		r.MaxOut = 16 << 10
	}

	code := r.Code
	if r.Path != "" {
		b, err := os.ReadFile(r.Path)
		if err != nil {
			res.Err = fmt.Errorf("read script: %w", err)
			return res
		}
		code = string(b)
		if r.Lang == "" {
			r.Lang = langFromExt(r.Path)
		}
	}
	if strings.TrimSpace(code) == "" {
		res.Err = fmt.Errorf("empty script")
		return res
	}

	if r.Lang == "" {
		r.Lang = DetectLang(code)
	}
	if r.Lang == "" {
		res.Err = fmt.Errorf("cannot detect language: pass --lang (available: %s)", strings.Join(Available(), ", "))
		return res
	}
	res.Lang = r.Lang
	rt, ok := runtimes[r.Lang]
	if !ok {
		res.Err = fmt.Errorf("unknown language %q (available: %s)", r.Lang, strings.Join(Available(), ", "))
		return res
	}
	binPath, err := exec.LookPath(rt.bin)
	if err != nil {
		res.Err = fmt.Errorf("runtime %q (%s) not installed", r.Lang, rt.bin)
		return res
	}
	res.Runtime = rt.bin

	dir, err := os.MkdirTemp("", "kern-exec-*")
	if err != nil {
		res.Err = err
		return res
	}
	defer func() { _ = os.RemoveAll(dir) }()

	src := filepath.Join(dir, "main"+rt.ext)
	if err := os.WriteFile(src, []byte(code), 0o600); err != nil {
		res.Err = err
		return res
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	// Defense in depth: an arbitrary agent call must never be able to drop
	// isolation and inherit os.Environ(). Only the local operator's explicit
	// KERN_ALLOW_NO_ISOLATE=1 flag makes NoIsolate effective; any other value
	// ("0", "true", ...) is treated as unset.
	if r.NoIsolate && os.Getenv("KERN_ALLOW_NO_ISOLATE") != "1" {
		r.NoIsolate = false
	}
	var ns []string
	if !r.NoIsolate {
		ns = networkNS()
		res.Isolated = ns != nil
		// Never silently degrade to full network egress — fail closed on every
		// platform. If isolation was requested but the netns/unshare path is
		// unavailable (macOS and Windows have no unprivileged user namespaces),
		// refuse to run rather than quietly exposing the full network, unless
		// the local operator explicitly opted into running unisolated via
		// KERN_ALLOW_UNISOLATED=1 (or the pre-existing alias KERN_ALLOW_NET=1).
		if ns == nil && !allowUnisolated() {
			res.Err = fmt.Errorf("network isolation not available on this platform (%s); refusing to run unisolated (fail-closed)\n"+
				"  to override and run without network isolation, set: export KERN_ALLOW_UNISOLATED=1 (or KERN_ALLOW_NET=1)\n"+
				"  to enable network isolation on Linux, run: sysctl -w kernel.unprivileged_userns_clone=1 (or use a Linux VM/container)", goruntime.GOOS)
			return res
		}
	}
	// Egress gate: declared "# egress:" targets (and structured Run.Egress
	// entries) are enforced on unisolated runs (a netns blocks egress
	// regardless). Fail closed under the operator's KERN_EGRESS_POLICY — the
	// NoIsolate path is gated too, since it runs with full network access.
	if err := egressGate(code, r.Egress, res.Isolated); err != nil {
		res.Err = err
		return res
	}
	env := sandboxEnv(dir)
	if r.NoIsolate {
		// Unisolated runs may inherit the operator's full environment, so
		// secret-carrying vars are stripped before spawn.
		env = governance.StripSecrets(os.Environ(), governance.DefaultSecretFilter())
	}

	// wrap prepends the unshare network namespace when available.
	wrap := func(cmd *exec.Cmd) *exec.Cmd {
		if len(ns) == 0 {
			return cmd
		}
		cmd.Path = ns[0]
		cmd.Args = append(append([]string{}, ns...), cmd.Args...)
		return cmd
	}

	if r.Lang == "rust" {
		// Two-step: compile then run the produced binary.
		cmd := wrap(exec.CommandContext(ctx, binPath, append(append([]string{}, rt.post...), src)...))
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(r.Stdin)
		cmd.Env = env
		processgroup.Set(cmd)
		cerr := &cappedBuffer{limit: 8 << 10}
		cmd.Stdout = cerr
		cmd.Stderr = cerr
		if err := cmd.Run(); err != nil {
			res.exitFrom(cmd, err, cerr.String(), "compile")
			return res
		}
		runCmd := wrap(exec.CommandContext(ctx, filepath.Join(dir, "prog")))
		runCmd.Dir = dir
		runCmd.Stdin = strings.NewReader(r.Stdin)
		runCmd.Env = env
		res.capture(ctx, runCmd, r.MaxOut, r.Timeout)
		return res
	}

	cmd := wrap(exec.CommandContext(ctx, binPath, append(append([]string{}, rt.pre...), src)...))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(r.Stdin)
	cmd.Env = env
	res.capture(ctx, cmd, r.MaxOut, r.Timeout)
	return res
}

// capture runs cmd, separating stdout (returned) from stderr (failure-only).
// stdout is read through a capped buffer so a runaway script cannot exhaust
// memory before the byte cap is applied. The command runs in its own process
// group so on timeout the whole group — including grandchildren — is killed.
func (res *Result) capture(ctx context.Context, cmd *exec.Cmd, maxOut int, timeout time.Duration) {
	out := &cappedBuffer{limit: maxOut}
	errb := &cappedBuffer{limit: 8 << 10} // stderr is truncated at 8 KiB anyway
	cmd.Stdout = out
	cmd.Stderr = errb
	processgroup.Set(cmd)
	err := cmd.Run()
	stdout := out.String()
	if len(stdout) > maxOut {
		stdout = stdout[:maxOut] + fmt.Sprintf("\n… [truncated at %d bytes]", maxOut)
		res.Truncated = true
	}
	res.Stdout = stdout
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		// The context kill only reaches the direct child; kill the process
		// group so any grandchildren the script spawned also die.
		processgroup.Kill(cmd)
		res.TimedOut = true
		res.Err = fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() == context.Canceled:
		processgroup.Kill(cmd)
		res.Err = fmt.Errorf("cancelled")
	case err != nil:
		res.exitFrom(cmd, err, errb.String(), "run")
	}
	if res.Err == nil {
		res.OK = true
	}
}

// cappedBuffer buffers up to limit bytes of output and discards (but still
// counts, so writes never block on a closed pipe) everything beyond it. It
// records a single extra byte so the caller can distinguish "exactly limit"
// from "truncated" without buffering the whole stream. This bounds memory for
// scripts that spew unbounded output.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	overLimit bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := c.limit + 1 - c.buf.Len()
	if room <= 0 {
		c.overLimit = true
		return n, nil
	}
	if n > room {
		p = p[:room]
		c.overLimit = true
	}
	c.buf.Write(p)
	// Always report the full input length so the exec copy loop does not see a
	// "short write" (it only errors when n < len(p) with a nil error).
	return n, nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func (res *Result) exitFrom(cmd *exec.Cmd, err error, stderr, stage string) {
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	} else {
		res.ExitCode = -1
		res.Err = fmt.Errorf("%s: %w", stage, err)
	}
	res.Stderr = truncate(stderr, 8<<10)
	if res.ExitCode == -1 {
		return
	}
	res.Err = fmt.Errorf("%s failed with exit code %d", stage, res.ExitCode)
	if res.Stderr != "" {
		res.Err = fmt.Errorf("%s failed with exit code %d: %s", stage, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… [truncated]"
}

// DetectLang infers a language from a shebang line, or from unambiguous
// content signals when there is no shebang. It looks for the runtime name
// after "#!...env" or as the base of an interpreter path. When no shebang is
// present, detectLangFromContent is consulted; if that also returns empty,
// the caller surfaces the "cannot detect language" error.
func DetectLang(code string) string {
	first := strings.TrimSpace(strings.SplitN(code, "\n", 2)[0])
	if strings.HasPrefix(first, "#!") {
		fields := strings.Fields(strings.TrimPrefix(first, "#!"))
		if len(fields) == 0 {
			return ""
		}
		candidate := fields[len(fields)-1]
		if candidate == "env" && len(fields) > 1 {
			candidate = fields[len(fields)-2]
		}
		base := candidate
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if _, ok := runtimes[base]; ok {
			return base
		}
		if _, ok := runtimes[candidate]; ok {
			return candidate
		}
		// /usr/bin/python3 → base python3; /usr/bin/env bash → bash.
		return ""
	}
	// No shebang: try content-based detection from the code itself.
	return detectLangFromContent(code)
}

// detectLangFromContent identifies a language from unambiguous content signals
// in the first few non-empty lines. This lets `kern exec "print(1)"` work
// without --lang by recognizing Python's print(), Go's package decl, Ruby's
// puts, Node's require/console, etc. Only when no language-specific signal is
// found does it fall back to bash — and only for content that looks shell-like
// (starts with a known shell builtin/command). Ambiguous content returns "" so
// the caller surfaces the "cannot detect" error rather than guessing wrong.
func detectLangFromContent(code string) string {
	lines := strings.Split(code, "\n")
	var firstLines []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		firstLines = append(firstLines, t)
		if len(firstLines) >= 5 {
			break
		}
	}
	if len(firstLines) == 0 {
		return ""
	}
	joined := strings.Join(firstLines, "\n")

	// Go: "package main" or "package foo" at the start is unambiguous.
	if regexp.MustCompile(`^package\s+\w`).MatchString(firstLines[0]) {
		return "go"
	}
	// Python: def, import, from, class, print( — but NOT "print" alone in
	// shell (shell print is rare). Combined with parens it's Python.
	if regexp.MustCompile(`^\s*(def\s+\w|import\s+\w|from\s+\w+\s+import|class\s+\w|print\s*\()`).MatchString(joined) {
		return "python3"
	}
	// Ruby: puts, def, require, print "..." with no parens.
	if regexp.MustCompile(`^\s*(puts\s|def\s+\w|require\s+['"])`).MatchString(joined) {
		return "ruby"
	}
	// Node: require('...'), console.log, const/let with => arrow.
	if regexp.MustCompile(`^\s*(const\s|let\s|var\s|require\s*\(|console\.)`).MatchString(joined) {
		return "node"
	}
	// Perl: use strict; use warnings; #!/usr/bin/perl
	if regexp.MustCompile(`^\s*(use\s+strict|use\s+warnings|use\s+\w+::)`).MatchString(joined) {
		return "perl"
	}
	// Lua: local x =, function, print(
	if regexp.MustCompile(`^\s*(local\s|function\s+\w)`).MatchString(joined) {
		return "lua"
	}
	// Rust: fn main, use std::
	if regexp.MustCompile(`^\s*(fn\s+main|use\s+std::)`).MatchString(joined) {
		return "rust"
	}
	// PHP: <?php
	if strings.HasPrefix(joined, "<?php") {
		return "php"
	}

	// No language-specific signal found. Default to bash only when bash is
	// installed and the first line looks like a shell command — a known
	// builtin/external followed by a space or end-of-line. This prevents
	// misclassifying Python/other code as bash (which produced confusing
	// syntax errors). If nothing matches, return "" so the caller errors
	// clearly with the available-runtimes list.
	if _, err := exec.LookPath("bash"); err == nil {
		shellCmds := regexp.MustCompile(`^(echo|ls|cd|pwd|cat|grep|sed|awk|cp|mv|rm|mkdir|touch|export|source|export|for\s|while\s|if\s|case\s|true|false|curl|wget|git|printf|test|find|xargs|sort|uniq|head|tail|tee|date|env|kill|ps|du|df|chmod|chown|tar|unzip|zip|make|docker|sudo|bash|sh|set|read|sleep|time|watch|basename|dirname|wc|cut|tr|paste|diff|patch|cmp|sha256sum|md5|nc|ping|ssh|scp|rsync|go|node|npm|npx|pip|python|python3)\b`)
		if shellCmds.MatchString(firstLines[0]) {
			return "bash"
		}
	}
	return ""
}

// langFromExt maps a file extension to a language name.
func langFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python3"
	case ".js", ".mjs":
		return "node"
	case ".ts":
		return "deno"
	case ".sh":
		return "bash"
	case ".pl":
		return "perl"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".lua":
		return "lua"
	case ".jl":
		return "julia"
	case ".rs":
		return "rust"
	case ".go":
		return "go"
	case ".r":
		return "R"
	}
	return ""
}

// Languages returns every supported language name, sorted.
func Languages() []string {
	out := make([]string, 0, len(runtimes))
	for name := range runtimes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
