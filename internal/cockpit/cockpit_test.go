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
