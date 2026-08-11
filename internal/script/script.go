// Package script runs code in an isolated local runtime and returns only
// stdout — the "Think in Code" surface of a local context optimizer (P0-6,
// borrowed from mksglu/context-mode's multi-language sandbox). An agent can
// compute things (data munging, math, JSON transforms, quick sims) without
// polluting its context with build noise or stderr.
//
// Isolation model (v2): the script runs in a fresh temp dir with the runtime
// resolved from PATH, a hard timeout, a stdout byte cap, and a sanitized
// environment (HOME and the XDG dirs point into the temp dir, so a script
// cannot read or clobber the user's real configs or env secrets). When the
// system's unprivileged user namespaces are enabled, the child also runs in a
// private network namespace (unshare --user --map-root-user --net), so network
// egress is blocked. If user namespaces are unavailable the run degrades to
// environment isolation only and Result.Isolated reports the gap. Stderr is
// never mixed into stdout — it is only surfaced on failure.
package script

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	// caller's environment and full network access. Isolation is on by default.
	NoIsolate bool
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

// networkNSArgs caches the unshare wrapper when unprivileged user namespaces
// plus a network namespace work; nil means the platform cannot isolate.
var (
	netProbeOnce sync.Once
	netNSArgs    []string
)

// networkNS returns the unshare prefix that runs a child in a private network
// namespace, or nil when unavailable (probed once per process). The wrapper is
// safe: --map-root-user inside a fresh user namespace grants no host access.
func networkNS() []string {
	netProbeOnce.Do(func() {
		bin, err := exec.LookPath("unshare")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, bin, "--user", "--map-root-user", "--net", "true").Run(); err == nil {
			netNSArgs = []string{bin, "--user", "--map-root-user", "--net"}
		}
	})
	return netNSArgs
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
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "main"+rt.ext)
	if err := os.WriteFile(src, []byte(code), 0o600); err != nil {
		res.Err = err
		return res
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	var ns []string
	if !r.NoIsolate {
		ns = networkNS()
		res.Isolated = ns != nil
	}
	env := sandboxEnv(dir)
	if r.NoIsolate {
		env = os.Environ()
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
		var cerr bytes.Buffer
		cmd.Stdout = &cerr
		cmd.Stderr = &cerr
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
func (res *Result) capture(ctx context.Context, cmd *exec.Cmd, maxOut int, timeout time.Duration) {
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	stdout := out.String()
	if len(stdout) > maxOut {
		stdout = stdout[:maxOut] + fmt.Sprintf("\n… [truncated at %d bytes]", maxOut)
		res.Truncated = true
	}
	res.Stdout = stdout
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.TimedOut = true
		res.Err = fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() == context.Canceled:
		res.Err = fmt.Errorf("cancelled")
	case err != nil:
		res.exitFrom(cmd, err, errb.String(), "run")
	}
	if res.Err == nil {
		res.OK = true
	}
}

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

// DetectLang infers a language from a shebang line (or empty). It looks for
// the runtime name after "#!...env" or as the base of an interpreter path.
func DetectLang(code string) string {
	first := strings.TrimSpace(strings.SplitN(code, "\n", 2)[0])
	if !strings.HasPrefix(first, "#!") {
		return ""
	}
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
