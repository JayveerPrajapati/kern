// Package sandbox provides isolated build/test execution for Blueprint's
// Phase 8 (Sandbox / Isolated Change Validation, spec Section 17).
//
// Blueprint uses git worktrees for filesystem isolation (separate working tree
// from the main repo) and Go's os/exec with process groups for process
// isolation (timeout, output cap, kill-on-cancel). This is NOT a second
// snapshot engine — it uses git's native worktree feature as the spec
// suggests ("sandbox/worktree mechanisms", line 1164).
//
// Security properties (spec lines 1194-1207):
//   - timeout: enforced via context deadline + process group kill
//   - output cap: stdout/stderr truncated to MaxOutputBytes
//   - workspace cleanup: worktree removed in a defer
//   - env inheritance: KERN_ALLOW_EXEC inherited, rest sanitized
//   - network isolation (opt-in): Config.NetworkIsolated runs the command in
//     a new network namespace on Linux (CLONE_NEWNET) or under a network-deny
//     sandbox-exec profile on macOS; on platforms without either, Run fails
//     closed unless Config.AllowUnisolated explicitly permits running without
//     isolation
//   - process cleanup: process group killed on timeout/cancel
//   - cancellation: context cancellation propagates to the process group
//   - deterministic exit: exit code captured even on signal
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Result holds the outcome of a sandboxed command execution.
type Result struct {
	Command   string        // the command that was run
	ExitCode  int           // process exit code (-1 if killed by signal)
	Ok        bool          // exit code == 0
	Duration  time.Duration // wall-clock duration
	Stdout    string        // truncated to MaxOutputBytes
	Stderr    string        // truncated to MaxOutputBytes
	Truncated bool          // true if output was capped
	TimedOut  bool          // true if killed by timeout
	Cancelled bool          // true if cancelled via context
	Error     string        // non-empty if the sandbox itself failed (not the command)
	Worktree  string        // path to the worktree (cleaned up after run)
}

// Config configures the sandbox execution.
type Config struct {
	Timeout        time.Duration // max execution time (0 = no timeout)
	MaxOutputBytes int           // cap on stdout+stderr combined (0 = no cap)
	Env            []string      // extra env vars (merged with a sanitized base)

	// NetworkIsolated, when true, isolates the sandboxed process from the
	// host network. On Linux the process runs in a new network namespace
	// (CLONE_NEWNET) containing only loopback — no external egress. On macOS
	// the command runs under sandbox-exec with a network-deny profile. On
	// Windows and other unsupported platforms Run fails closed unless
	// AllowUnisolated is also set (see below). Opt-in; default OFF.
	NetworkIsolated bool

	// AllowUnisolated, when true, permits running WITHOUT network isolation
	// when isolation was explicitly requested (NetworkIsolated) but this
	// platform cannot provide it (non-Linux). Without this override, Run
	// fails closed with an error instead of silently downgrading a requested
	// safety control. An overridden unisolated run is never silent: a warning
	// is still printed to stderr. No effect when isolation is available or
	// was not requested. Opt-in; default OFF.
	AllowUnisolated bool

	// Matrix defines an optional polyglot build/test matrix.
	Matrix []MatrixTarget
}

// MatrixTarget defines one target component in a polyglot build/test matrix.
type MatrixTarget struct {
	Name    string   `json:"name"`
	Dir     string   `json:"dir"`     // relative to repo root, default "."
	Build   []string `json:"build"`   // build command argv
	Test    []string `json:"test"`    // test command argv
	Command []string `json:"command"` // combined command argv
}

// WithMatrix sets the polyglot build/test matrix.
func WithMatrix(matrix []MatrixTarget) ConfigOption {
	return func(c *Config) { c.Matrix = matrix }
}

// WithTimeout sets the execution timeout.
func WithTimeout(d time.Duration) ConfigOption {
	return func(c *Config) { c.Timeout = d }
}

// DefaultConfig returns safe defaults: 120s timeout, 1MiB output cap.
func DefaultConfig() Config {
	timeout := 120 * time.Second
	if env := os.Getenv("BLUEPRINT_SANDBOX_TIMEOUT"); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			timeout = d
		}
	}
	return Config{
		Timeout:        timeout,
		MaxOutputBytes: 1 << 20, // 1 MiB
	}
}

// Run executes the given command in an isolated git worktree of repoRoot.
// The worktree is created at a temp path, the command runs there, and the
// worktree is removed afterward. The main repo is never touched.
func Run(ctx context.Context, repoRoot string, command []string, cfg Config) Result {
	worktreePath, cleanup, err := createWorktree(repoRoot)
	if err != nil {
		return Result{
			Command: strings.Join(command, " "),
			Error:   fmt.Sprintf("create worktree: %v", err),
		}
	}
	defer cleanup()

	r := RunInWorktree(ctx, worktreePath, "", command, cfg)
	r.Worktree = worktreePath
	return r
}

// RunInWorktree executes command in an existing worktree directory (or a subdirectory).
func RunInWorktree(ctx context.Context, worktreePath, subDir string, command []string, cfg Config) Result {
	start := time.Now()
	result := Result{Command: strings.Join(command, " "), Worktree: worktreePath}

	if len(command) == 0 {
		result.Error = "no command specified"
		return result
	}

	if cfg.NetworkIsolated && !networkIsolationAvailable() && !cfg.AllowUnisolated {
		result.Error = "network isolation requested but unavailable on this platform; use --allow-unisolated to override"
		return result
	}

	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	dir := worktreePath
	if subDir != "" && subDir != "." {
		dir = filepath.Join(worktreePath, subDir)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = sanitizedEnv(cfg.Env)
	setProcessGroup(cmd)

	// Optional network isolation: on Linux the command runs in a new network
	// namespace (CLONE_NEWNET) with only loopback; on macOS it runs under
	// sandbox-exec with a network-deny profile. On platforms without
	// isolation support, reaching here means the caller explicitly overrode
	// the fail-closed gate (AllowUnisolated) — applyNetworkIsolation prints a
	// visible warning and the command runs unisolated.
	if cfg.NetworkIsolated {
		applyNetworkIsolation(cmd)
	}

	// Capture output with a cap.
	var stdoutBuf, stderrBuf bytes.Buffer
	maxOut := cfg.MaxOutputBytes
	if maxOut == 0 {
		maxOut = 1 << 20
	}
	limitedStdout := &cappedWriter{w: &stdoutBuf, max: maxOut / 2}
	limitedStderr := &cappedWriter{w: &stderrBuf, max: maxOut / 2}
	cmd.Stdout = limitedStdout
	cmd.Stderr = limitedStderr

	// Start the command.
	if err := cmd.Start(); err != nil {
		result.Error = fmt.Sprintf("start command: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// Watch the context in a goroutine. On timeout/cancel, kill the entire
	// process group so child processes (spawned by `go run`/`go test`) are
	// reaped and their pipes close, allowing cmd.Wait() to return promptly.
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	select {
	case err := <-waitErr:
		// Process exited on its own.
		result.Duration = time.Since(start)
		result.Stdout = stdoutBuf.String()
		result.Stderr = stderrBuf.String()
		result.Truncated = limitedStdout.capped || limitedStderr.capped
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
				if result.Error == "" {
					result.Error = err.Error()
				}
			}
		} else {
			result.ExitCode = 0
			result.Ok = true
		}
	case <-ctx.Done():
		// Context timed out or was cancelled — kill the process group.
		killProcessGroup(cmd)
		err := <-waitErr // wait for the kill to take effect
		result.Duration = time.Since(start)
		result.Stdout = stdoutBuf.String()
		result.Stderr = stderrBuf.String()
		result.Truncated = limitedStdout.capped || limitedStderr.capped
		if ctx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
		} else {
			result.Cancelled = true
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
			}
		}
	}

	return result
}

// createWorktree creates a detached git worktree of repoRoot at a temp path.
// Returns the worktree path and a cleanup function.
func createWorktree(repoRoot string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "blueprint-sandbox-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	worktreePath := filepath.Join(tmpDir, "work")

	// Create a worktree from the current HEAD.
	cmd := exec.Command("git", "worktree", "add", "--detach", worktreePath, "HEAD")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("git worktree add: %w: %s", err, string(out))
	}

	cleanup := func() {
		// Remove the worktree registration from the main repo.
		rmCmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
		rmCmd.Dir = repoRoot
	_ = rmCmd.Run() // best-effort
	// Also prune stale worktree metadata.
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoRoot
	_ = pruneCmd.Run()
		// Remove the temp dir.
		os.RemoveAll(tmpDir)
	}

	return worktreePath, cleanup, nil
}

// CreateWorktree creates a detached git worktree of repoRoot at a fresh temp
// path and returns the worktree path plus a cleanup function that removes the
// worktree registration and the temp dir. It is the exported form of
// createWorktree; cmd/blueprint fix uses it to validate agent-proposed
// content against an isolated copy of the repo (the user's tree is never
// written to).
func CreateWorktree(repoRoot string) (string, func(), error) {
	return createWorktree(repoRoot)
}

// sanitizedEnv returns a clean environment for the sandboxed process.
// KERN_ALLOW_EXEC is inherited (so kern validate works inside the worktree),
// but PATH and HOME are preserved. Other env vars from cfg.Env are added.
func sanitizedEnv(extra []string) []string {
	base := []string{}
	for _, kv := range os.Environ() {
		// Keep PATH, HOME, USER, LANG, LC_*, and KERN_ALLOW_EXEC.
		if strings.HasPrefix(kv, "PATH=") ||
			strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "USER=") ||
			strings.HasPrefix(kv, "LANG=") ||
			strings.HasPrefix(kv, "LC_") ||
			strings.HasPrefix(kv, "KERN_ALLOW_EXEC=") ||
			strings.HasPrefix(kv, "GOPATH=") ||
			strings.HasPrefix(kv, "GOROOT=") ||
			strings.HasPrefix(kv, "GOCACHE=") ||
			strings.HasPrefix(kv, "GOMODCACHE=") {
			base = append(base, kv)
		}
	}
	base = append(base, extra...)
	return base
}

// cappedWriter wraps a writer and stops writing after max bytes.
type cappedWriter struct {
	w       *bytes.Buffer
	max     int
	written int
	capped  bool
}

func (cw *cappedWriter) Write(p []byte) (int, error) {
	if cw.capped {
		return 0, nil
	}
	remaining := cw.max - cw.written
	if len(p) > remaining {
		cw.w.Write(p[:remaining])
		cw.capped = true
		return remaining, nil
	}
	cw.written += len(p)
	return cw.w.Write(p)
}
