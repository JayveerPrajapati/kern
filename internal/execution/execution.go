// Package execution is the 2.0 execution/sandbox layer (Phase 8 of the Kern 2.0
// migration; see spec §15). It wraps internal/sandbox into a higher-level
// service combining isolated worktrees, snapshot+rollback execution, test/build
// runs, and artifact collection.
//
// SECURITY: Execute and its helpers run arbitrary host commands and must never
// be exposed to an ungoverned caller. New call surfaces that run commands
// through an Executor MUST apply the governance firewall (governance.CheckExec)
// first; autonomy/approval checks live in the caller's governance layer.
package execution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
)

// Executor runs commands safely: snapshot, execute, rollback on failure, keep
// on success.
//
// SECURITY: an Executor can enforce a governance gate before every command via
// WithGovernance or WithExecFirewall. By default the gate is nil and the
// executor relies on the caller's call-site gating. When a gate is set and
// denies a command, Execute refuses to run it (fail closed).
type Executor struct {
	root  string
	check func(command string, args []string) error
}

// NewExecutor creates an executor for the given project root.
func NewExecutor(root string) *Executor {
	return &Executor{root: root}
}

// WithGovernance attaches an optional pre-execution gate to the executor.
// When set, Execute refuses to run a command if the gate returns a non-nil
// error; a nil gate performs no executor-level check. Returns the executor for
// chaining.
func (e *Executor) WithGovernance(check func(command string, args []string) error) *Executor {
	if e != nil {
		e.check = check
	}
	return e
}

// WithExecFirewall attaches the repo's governance firewall to the executor so
// every Execute call passes through governance.CheckExec. Callers that already
// gate at their call site may leave the executor unconfigured to avoid
// double-gating.
func (e *Executor) WithExecFirewall() *Executor {
	return e.WithGovernance(func(command string, args []string) error {
		return governance.CheckExec()
	})
}

// Root returns the project root the executor operates on.
func (e *Executor) Root() string {
	return e.projectRoot()
}

// projectRoot returns the executor's root, nil-safe.
func (e *Executor) projectRoot() string {
	if e == nil {
		return ""
	}
	return e.root
}

// Result is the outcome of an execution.
type Result struct {
	OK        bool
	ExitCode  int
	Output    string
	Err       error
	Duration  time.Duration
	Restored  bool
	Snapshots int
	Root      string // project root used
}

// Execute runs a command in a sandbox: snapshots root, runs cmd, restores on
// failure. command is the executable name (e.g. "go"), args its arguments
// (e.g. ["test", "./..."]). timeout limits the execution; a non-positive
// timeout means no explicit limit beyond the sandbox run.
func (e *Executor) Execute(command string, args []string, timeout time.Duration) Result {
	metrics.Default().RecordSandboxOp()
	start := time.Now()
	defer func() { metrics.Default().RecordToolCall(time.Since(start)) }()

	if args == nil {
		args = []string{}
	}
	root := e.projectRoot()
	// When a governance gate is configured, refuse to run the command if the
	// gate denies it (fail closed).
	if e != nil && e.check != nil {
		if err := e.check(command, args); err != nil {
			return Result{OK: false, Err: err, Root: root}
		}
	}
	r := sandbox.Run(context.Background(), root, command, args, timeout)
	return Result{
		OK:        r.OK,
		ExitCode:  r.ExitCode,
		Output:    r.Output,
		Err:       r.Err,
		Duration:  r.Duration,
		Restored:  r.Restored,
		Snapshots: r.Snapshots,
		Root:      root,
	}
}

// buildCommand returns the shell-free command name and args used to build a
// project at root. Go projects use "go build ./..."; projects with a Makefile
// use "make" (with no args). A missing (nil) slice means no args.
func buildCommand(root string) (string, []string, error) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return "go", []string{"build", "./..."}, nil
	}
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		return "make", []string{}, nil
	}
	return "", nil, fmt.Errorf("no build command detected: expected go.mod (go build ./...) or Makefile (make)")
}

// testCommand is like buildCommand but for the test suite.
func testCommand(root string) (string, []string, error) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return "go", []string{"test", "./..."}, nil
	}
	return "", nil, fmt.Errorf("no test command detected: expected go.mod (go test ./...)")
}

// ExecuteBuild runs the project's build command in a sandbox.
// Returns Result with OK set on success.
func (e *Executor) ExecuteBuild(timeout time.Duration) Result {
	metrics.Default().RecordSandboxOp()
	cmd, args, err := buildCommand(e.projectRoot())
	if err != nil {
		return Result{Err: err, Root: e.projectRoot()}
	}
	return e.Execute(cmd, args, timeout)
}

// ExecuteTests runs the project's test command in a sandbox.
// Returns Result with OK set on success.
func (e *Executor) ExecuteTests(timeout time.Duration) Result {
	metrics.Default().RecordSandboxOp()
	cmd, args, err := testCommand(e.projectRoot())
	if err != nil {
		return Result{Err: err, Root: e.projectRoot()}
	}
	return e.Execute(cmd, args, timeout)
}
