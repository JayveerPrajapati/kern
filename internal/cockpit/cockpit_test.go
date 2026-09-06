package cockpit

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/loop"
)

func TestCockpitInitialState(t *testing.T) {
	s := NewInitialState("task_1", "Test Intent", "/tmp/repo")
	if s.TaskID != "task_1" {
		t.Errorf("expected task_1, got %s", s.TaskID)
	}
	if len(s.Gates) != 30 {
		t.Errorf("expected 30 gates registered, got %d", len(s.Gates))
	}
	if len(s.Phases) != len(OrderedPhases) {
		t.Errorf("expected %d phases, got %d", len(OrderedPhases), len(s.Phases))
	}
}

func TestCockpitRenderOutput(t *testing.T) {
	s := NewInitialState("task_1", "Implement JWT rotation in auth middleware", "/tmp/repo")
	s.Diff = "diff --git a/auth/jwt.go b/auth/jwt.go\n+func RotateJWT() {}\n-func OldJWT() {}\n"
	s.TokensUsed = 1200
	s.CostDollars = 0.0036
	s.Gates["G0"].Status = StatusPass
	s.Gates["G1"].Status = StatusPass
	s.Gates["G2"].Status = StatusRepairing

	out := RenderCockpit(s, 90)

	if !strings.Contains(out, "KERNOPS COCKPIT") {
		t.Errorf("expected title in cockpit render")
	}
	if !strings.Contains(out, "G0") || !strings.Contains(out, "[PASS]") {
		t.Errorf("expected G0 PASS in gate grid, got:\n%s", out)
	}
	if !strings.Contains(out, "G2") || !strings.Contains(out, "[REPAIR]") {
		t.Errorf("expected G2 REPAIR in gate grid, got:\n%s", out)
	}
	if !strings.Contains(out, "RotateJWT") {
		t.Errorf("expected diff content in render, got:\n%s", out)
	}
	if !strings.Contains(out, "Tokens: 1200 used") {
		t.Errorf("expected token metric in render, got:\n%s", out)
	}
}

func TestCockpitRunnerNonInteractive(t *testing.T) {
	tmp := t.TempDir()
	var buf bytes.Buffer

	cfg := RunnerConfig{
		RepoRoot:       tmp,
		TaskPrompt:     "refactor auth middleware",
		AutonomyLevel:  loop.L3,
		NonInteractive: true,
		Output:         &buf,
		StepOverride: func(stage, intent string, wt *execution.Worktree, res *loop.Result) (string, error) {
			return "done", nil
		},
	}

	runner := NewRunner(cfg)
	state, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}

	if !state.Success {
		t.Errorf("expected successful run")
	}
	output := buf.String()
	if !strings.Contains(output, "[KERNOPS] Starting task") {
		t.Errorf("expected starting task log, got: %s", output)
	}
	if !strings.Contains(output, "[KERNOPS] SUCCESS") {
		t.Errorf("expected success log, got: %s", output)
	}
}

// TestCockpitRunnerHonorsExplicitLevels protects against the L0 sentinel bug
// (A1): an explicitly requested L0 (the zero value) must not be silently
// upgraded to the default L3. Every level L0..L5 must pass through unchanged.
func TestCockpitRunnerHonorsExplicitLevels(t *testing.T) {
	for _, level := range []loop.Autonomy{loop.L0, loop.L1, loop.L2, loop.L3, loop.L4, loop.L5} {
		t.Run(level.String(), func(t *testing.T) {
			var buf bytes.Buffer
			cfg := RunnerConfig{
				RepoRoot:       t.TempDir(),
				TaskPrompt:     "probe",
				AutonomyLevel:  level,
				NonInteractive: true,
				Output:         &buf,
				StepOverride: func(stage, intent string, wt *execution.Worktree, res *loop.Result) (string, error) {
					return "done", nil
				},
			}
			runner := NewRunner(cfg)
			state, err := runner.Run(context.Background())
			if err != nil {
				t.Fatalf("Runner.Run: %v", err)
			}
			if state.AutonomyLevel != level.String() {
				t.Errorf("AutonomyLevel = %q, want %q (explicit level must not be upgraded)", state.AutonomyLevel, level.String())
			}
			if !strings.Contains(buf.String(), "(Level: "+level.String()+")") {
				t.Errorf("expected run log to honor level %s, got: %s", level.String(), buf.String())
			}
		})
	}
}

// TestCockpitRunnerDefaultsToL3 verifies the sentinel (unset) level still
// resolves to L3, preserving the documented default for callers who do not
// pin an explicit level. An explicit L0 must NOT take this path (the zero
// value is read-only L0, and the sentinel is what triggers the L3 default).
func TestCockpitRunnerDefaultsToL3(t *testing.T) {
	var buf bytes.Buffer
	cfg := RunnerConfig{
		RepoRoot:       t.TempDir(),
		TaskPrompt:     "probe",
		AutonomyLevel:  loop.AutonomyUnset,
		NonInteractive: true,
		Output:         &buf,
		StepOverride: func(stage, intent string, wt *execution.Worktree, res *loop.Result) (string, error) {
			return "done", nil
		},
	}
	runner := NewRunner(cfg)
	state, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if state.AutonomyLevel != loop.L3.String() {
		t.Errorf("AutonomyLevel = %q, want default L3 for unset level", state.AutonomyLevel)
	}
}

// TestCockpitZeroValueIsReadOnly guards the safe-default invariant: a fully
// zero-value RunnerConfig (AutonomyLevel unset by the caller) must resolve to
// read-only L0, never an un-pinned level that mutates the workspace.
func TestCockpitZeroValueIsReadOnly(t *testing.T) {
	var buf bytes.Buffer
	cfg := RunnerConfig{
		RepoRoot:       t.TempDir(),
		TaskPrompt:     "probe",
		NonInteractive: true,
		Output:         &buf,
		StepOverride: func(stage, intent string, wt *execution.Worktree, res *loop.Result) (string, error) {
			return "done", nil
		},
	}
	runner := NewRunner(cfg)
	state, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if state.AutonomyLevel != loop.L0.String() {
		t.Errorf("AutonomyLevel = %q, want read-only L0 for zero-value RunnerConfig", state.AutonomyLevel)
	}
}
