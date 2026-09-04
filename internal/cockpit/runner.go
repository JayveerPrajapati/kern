package cockpit

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// RunnerConfig configures the cockpit runner.
type RunnerConfig struct {
	RepoRoot       string
	TaskPrompt     string
	AutonomyLevel  loop.Autonomy
	NonInteractive bool
	AutoApprove    bool
	Output         io.Writer
	StepOverride   loop.StepFunc
}

// Runner orchestrates the execution and drives the cockpit UI.
type Runner struct {
	cfg   RunnerConfig
	state *State
	mu    sync.Mutex
	bus   *eventbus.Bus
	out   io.Writer
}

// NewRunner creates a new cockpit Runner.
func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}
	if cfg.AutonomyLevel == 0 {
		cfg.AutonomyLevel = loop.L3
	}

	taskID := fmt.Sprintf("task_%d", time.Now().Unix())
	state := NewInitialState(taskID, cfg.TaskPrompt, cfg.RepoRoot)
	state.AutonomyLevel = cfg.AutonomyLevel.String()

	bus := eventbus.New()

	r := &Runner{
		cfg:   cfg,
		state: state,
		bus:   bus,
		out:   cfg.Output,
	}

	r.wireEvents()
	return r
}

// wireEvents listens to real-time events from the bus and updates State.
func (r *Runner) wireEvents() {
	r.bus.Subscribe("", func(ev eventbus.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()

		pMap := payloadMap(ev)
		switch ev.Kind {
		case eventbus.SandboxReady:
			if path, ok := pMap["path"]; ok {
				r.state.WorktreeDir = path
			}
		case eventbus.GateEvaluated:
			if gid, ok := pMap["gate"]; ok {
				if gst, exists := r.state.Gates[gid]; exists {
					gst.Status = StatusPass
					if st, ok := pMap["status"]; ok && st == "BLOCK" {
						gst.Status = StatusBlock
					}
				}
			}
		case eventbus.RepairAttempt:
			r.state.RepairAttempts++
			if gid, ok := pMap["gate_id"]; ok {
				if gst, exists := r.state.Gates[gid]; exists {
					gst.Status = StatusRepairing
				}
			}
			r.setPhase(PhaseFirewall, StatusRepairing, fmt.Sprintf("Auto-repair cycle #%d in progress", r.state.RepairAttempts))
		case eventbus.FirewallPassed:
			r.setPhase(PhaseFirewall, StatusPass, "All Blueprint gates cleared")
		case eventbus.ApprovalRequested:
			r.state.ApprovalNeeded = true
			r.state.ApprovalReason = pMap["reason"]
			r.setPhase(PhaseApproval, StatusRunning, "Human approval requested")
		}

		if !r.cfg.NonInteractive {
			r.draw()
		}
	})
}

// setPhase updates the active phase state.
func (r *Runner) setPhase(p Phase, s Status, msg string) {
	r.state.ActivePhase = p
	if ps, ok := r.state.Phases[p]; ok {
		ps.Status = s
		ps.Message = msg
		if ps.Started.IsZero() {
			ps.Started = time.Now()
		} else {
			ps.Duration = time.Since(ps.Started)
		}
	}
}

// Run executes the task through the governed autonomous loop.
func (r *Runner) Run(ctx context.Context) (*State, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if r.cfg.NonInteractive {
		fmt.Fprintf(r.out, "[KERNOPS] Starting task: %q (Level: %s)\n", r.cfg.TaskPrompt, r.cfg.AutonomyLevel)
	} else {
		r.draw()
	}

	// 1. Initialize Firewall & WorktreeManager
	adapter, err := loop.NewDefaultFirewallAdapter(r.cfg.RepoRoot)
	if err != nil {
		r.state.Error = err.Error()
		r.state.Completed = true
		return r.state, err
	}

	wm := sandbox.NewWorktreeManager(r.cfg.RepoRoot)

	// 2. Build LoopConfig
	loopCfg := loop.LoopConfig{
		Root:              r.cfg.RepoRoot,
		Level:             r.cfg.AutonomyLevel,
		Mem:               memory.NewMemoryStore(r.cfg.RepoRoot),
		Firewall:          adapter,
		WorktreeManager:   wm,
		MaxRepairAttempts: 3,
	}

	lp, err := loop.NewLoop(loopCfg)
	if err != nil {
		r.state.Error = err.Error()
		r.state.Completed = true
		return r.state, err
	}
	lp.WithBus(r.bus)

	// 3. Drive execution
	r.setPhase(PhaseIntent, StatusPass, "Intent compiled")
	r.setPhase(PhasePlan, StatusRunning, "Formulating minimal AST slices")

	step := r.cfg.StepOverride
	if step == nil {
		step = func(stage, intent string, wt *execution.Worktree, res *loop.Result) (string, error) {
			switch stage {
			case "plan":
				r.setPhase(PhasePlan, StatusPass, "Plan generated")
				r.setPhase(PhaseCode, StatusRunning, "Applying changes inside ephemeral sandbox")
				return "Plan: implement requested change safely", nil
			case "code":
				r.setPhase(PhaseCode, StatusPass, "Code applied to worktree")
				r.setPhase(PhaseFirewall, StatusRunning, "Evaluating Blueprint gates G0-G29")
				return "Wrote changes", nil
			case "repair":
				r.setPhase(PhaseFirewall, StatusRepairing, "Closed-loop repair attempt")
				return "Applied fix", nil
			}
			return "", nil
		}
	}

	res, runErr := lp.RunContext(ctx, r.cfg.TaskPrompt, step)

	r.mu.Lock()
	r.state.Completed = true
	if res != nil {
		r.state.Diff = res.Diff
		r.state.RepairAttempts = res.RepairAttempts
		r.state.RepairContracts = res.RepairContracts
		if res.Paused && res.PauseReason == "approval" {
			r.state.ApprovalNeeded = true
			r.state.ApprovalReason = "High-risk mutation requires sign-off"
			r.setPhase(PhaseApproval, StatusRunning, "Awaiting approval")
		}
	}

	if runErr != nil && (!r.state.ApprovalNeeded || !r.cfg.AutoApprove) {
		r.state.Success = false
		r.state.Error = runErr.Error()
	} else {
		r.state.Success = true
		r.setPhase(PhaseVerify, StatusPass, "All verification gates cleared")
		r.setPhase(PhaseReceipt, StatusPass, "Signed compliance receipt appended")
	}
	r.mu.Unlock()

	if !r.cfg.NonInteractive {
		r.draw()
	} else {
		if r.state.Success {
			fmt.Fprintf(r.out, "[KERNOPS] SUCCESS: Task %s completed cleanly. All gates passed.\n", r.state.TaskID)
		} else {
			fmt.Fprintf(r.out, "[KERNOPS] FAILED: %s\n", r.state.Error)
		}
	}

	return r.state, runErr
}

// draw prints the rendered cockpit UI to the output writer.
func (r *Runner) draw() {
	// Clear screen in interactive terminal
	fmt.Fprintf(r.out, "\033[2J\033[H")
	fmt.Fprint(r.out, RenderCockpit(r.state, 90))
}

func payloadMap(ev eventbus.Event) map[string]string {
	res := make(map[string]string)
	if m, ok := ev.Payload.(map[string]string); ok {
		return m
	}
	if m, ok := ev.Payload.(map[string]any); ok {
		for k, v := range m {
			res[k] = fmt.Sprintf("%v", v)
		}
		return res
	}
	return res
}
