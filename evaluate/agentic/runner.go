package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RunOutcome is the scored result of one (arm, task) execution.
type RunOutcome struct {
	Arm      string `json:"arm"`
	Task     string `json:"task"`
	Pass     bool   `json:"pass"`
	TimedOut bool   `json:"timed_out"`
	Exit     int    `json:"exit"`
	// DurationSec is wall-clock agent time; -1 when unknown (rescored rows
	// carry the original run's value read back from metrics.json).
	DurationSec  float64 `json:"duration_sec"`
	FilesChanged int     `json:"files_changed"`
	NonTestLOC   int     `json:"non_test_loc"`
	TestLOC      int     `json:"test_loc"`
	Usage        Usage   `json:"usage"`
}

// procResult is the raw outcome of one supervised process launch.
type procResult struct {
	exit     int
	timedOut bool
	dur      time.Duration
}

// runProc launches argv under a hard context deadline. On timeout the whole
// process group is killed (the agent may have spawned children). stdout and
// stderr stream to the callers' sinks so raw artifacts are captured even
// when the run is killed.
func runProc(ctx context.Context, argv []string, dir string, env []string, stdout, stderr io.Writer) (procResult, error) {
	if len(argv) == 0 {
		return procResult{}, errors.New("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if env != nil {
		cmd.Env = env
	}
	setGroupAttr(cmd)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return procResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		res := procResult{dur: time.Since(start)}
		if werr == nil {
			return res, nil
		}
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			res.exit = ee.ExitCode()
			return res, nil
		}
		return res, werr
	case <-ctx.Done():
		killGroup(cmd)
		<-done
		return procResult{timedOut: true, dur: time.Since(start)}, nil
	}
}

// runAll executes every (arm, task) pair sequentially and returns the
// scored outcomes. Sequential on purpose in v1: no API-rate interference and
// easy-to-read logs.
func runAll(ctx context.Context, arms []Arm, tasks []Task, runDir string, defaultTimeout time.Duration) ([]RunOutcome, error) {
	var out []RunOutcome
	for _, arm := range arms {
		for _, task := range tasks {
			oc, err := runOne(ctx, arm, task, runDir, defaultTimeout)
			if err != nil {
				return nil, err
			}
			out = append(out, oc)
		}
	}
	return out, nil
}

func runOne(ctx context.Context, arm Arm, task Task, runDir string, defaultTimeout time.Duration) (RunOutcome, error) {
	base := filepath.Join(runDir, arm.Name, task.Name)
	ws := filepath.Join(base, "ws")
	art := filepath.Join(base, "art")
	if err := os.MkdirAll(art, 0o755); err != nil {
		return RunOutcome{}, err
	}
	if err := copyDir(task.Fixture, ws); err != nil {
		return RunOutcome{}, fmt.Errorf("task %s: copy fixture: %w", task.Name, err)
	}

	timeout := defaultTimeout
	if task.Timeout > 0 {
		timeout = task.Timeout
	}
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	events, err := os.Create(filepath.Join(art, "events.jsonl"))
	if err != nil {
		return RunOutcome{}, err
	}
	errlog, err := os.Create(filepath.Join(art, "stderr.log"))
	if err != nil {
		events.Close()
		return RunOutcome{}, err
	}
	pr, err := runProc(actx, arm.argv(task.Prompt), ws, withEnv(arm.envPairs()), events, errlog)
	events.Close()
	errlog.Close()
	if err != nil {
		return RunOutcome{}, fmt.Errorf("arm %s task %s: %w", arm.Name, task.Name, err)
	}

	oc := RunOutcome{
		Arm:         arm.Name,
		Task:        task.Name,
		TimedOut:    pr.timedOut,
		Exit:        pr.exit,
		DurationSec: pr.dur.Seconds(),
		Usage:       extractUsage(mustRead(filepath.Join(art, "events.jsonl"))),
	}

	// Score the workspace delta BEFORE running check.sh so the check's own
	// side effects cannot pollute the over-engineering proxy.
	if d := workspaceDelta(task.Fixture, ws); d != nil {
		oc.FilesChanged, oc.NonTestLOC, oc.TestLOC = d.FilesChanged, d.NonTestLOC, d.TestLOC
	}
	if !pr.timedOut {
		oc.Pass = runCheck(task, ws, art)
	}

	if err := writeJSON(filepath.Join(art, "metrics.json"), oc); err != nil {
		return RunOutcome{}, err
	}
	return oc, nil
}

// runCheck executes the task's deterministic gate (cwd = workspace copy)
// with a bounded timeout; exit 0 = pass. Output is captured to
// art/check-output.log, never into the scored workspace.
func runCheck(task Task, ws, art string) bool {
	logf, err := os.Create(filepath.Join(art, "check-output.log"))
	if err != nil {
		return false
	}
	defer logf.Close()
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	pr, err := runProc(ctx, []string{task.Check}, ws, nil, logf, logf)
	if err != nil {
		fmt.Fprintf(logf, "run check.sh: %v (is it executable?)\n", err)
		return false
	}
	return pr.exit == 0 && !pr.timedOut
}

func withEnv(extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	return append(os.Environ(), extra...)
}

func mustRead(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

// copyDir recursively copies src to dst, preserving file modes (including
// the exec bit check.sh relies on).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			// The fixture root itself: create the destination dir.
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
