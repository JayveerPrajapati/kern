package loop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// failingDeployer is a Deployer that always fails — used to prove the
// rollback path: a failed deployment must surface as rolled back, never as a
// silent success.
type failingDeployer struct{}

func (failingDeployer) Deploy(ctx context.Context, req deployment.DeployRequest) (deployment.DeployResult, error) {
	return deployment.DeployResult{}, errors.New("injected deploy failure")
}

// TestAutonomyExitGate is the exit gate: autonomy passes FAILURE,
// SECURITY, ROLLBACK, BUDGET, and POLICY-BYPASS tests. Each sub-test exercises
// one dimension at the loop level so a regression in any guard is caught here.
func TestAutonomyExitGate(t *testing.T) {
	t.Run("failure_surfaces_error", func(t *testing.T) {
		// A stage that fails must surface the error and stop — no silent
		// success, no deployment.
		lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Mem: memory.NewMemoryStore(t.TempDir())})
		if err != nil {
			t.Fatalf("NewLoop: %v", err)
		}
		_, err = lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, res *Result) (string, error) {
			if stage == stageCode {
				return "", errors.New("injected stage failure")
			}
			return "ok", nil
		})
		if err == nil {
			t.Fatal("loop succeeded despite a failing stage — failure silently swallowed")
		}
		if !strings.Contains(err.Error(), "injected stage failure") {
			t.Fatalf("error = %v, want the stage failure surfaced", err)
		}
	})

	t.Run("security_L5_proof_gate", func(t *testing.T) {
		// L5 autonomy requires proofs: write/act stages are DENIED without
		// them (fail-closed), allowed only when all proofs are satisfied.
		l5, err := ParseLevel("L5")
		if err != nil {
			t.Fatal(err)
		}
		if l5.AllowsStageWithProofs(stageCode, L5Proofs{}) {
			t.Error("L5 without proofs allowed code — security gate bypassed")
		}
		if l5.AllowsStageWithProofs(stageDeploy, L5Proofs{}) {
			t.Error("L5 without proofs allowed deploy — security gate bypassed")
		}
		if !l5.AllowsStageWithProofs(stageCode, L5Proofs{
			ProofPolicy: true, ProofVerification: true, ProofRollback: true,
			ProofMonitoring: true, ProofAudit: true, ProofConfidence: true,
		}) {
			t.Error("L5 with all proofs denied code")
		}
	})

	t.Run("rollback_on_failed_deploy", func(t *testing.T) {
		// A failing deployer must roll back (res.Deployed=false, rolled-back
		// event, error surfaced) — never a silent success.
		t.Setenv("KERN_ALLOW_DEPLOY", "1")
		bus := eventbus.New()
		gotRollback := false
		bus.Subscribe(eventbus.DeploymentRolledBack, func(ev eventbus.Event) {
			gotRollback = true
		})
		lp, err := NewLoop(LoopConfig{
			Root:     loopFixture(t),
			Level:    L4,
			Service:  "svc",
			Mem:      memory.NewMemoryStore(t.TempDir()),
			Deployer: failingDeployer{},
		})
		if err != nil {
			t.Fatalf("NewLoop: %v", err)
		}
		lp.WithBus(bus)
		res, err := lp.Run("deploy something", nil)
		if err == nil {
			t.Fatal("loop succeeded despite a failing deployer")
		}
		if res.Deployed {
			t.Error("res.Deployed = true after a failed deployment — rollback bypassed")
		}
		if !gotRollback {
			t.Error("no DeploymentRolledBack event on failed deployment")
		}
	})

	t.Run("budget_pause", func(t *testing.T) {
		// Exceeding the safety budget must PAUSE the loop.
		budget := &domain.SafetyBudget{MaxToolCalls: 1}
		lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Mem: memory.NewMemoryStore(t.TempDir()), Budget: budget})
		if err != nil {
			t.Fatalf("NewLoop: %v", err)
		}
		// A step that tracks many tool calls will exceed the budget of 1.
		res, err := lp.Run("budgeted change", func(stage, intent string, wt *execution.Worktree, res *Result) (string, error) {
			for i := 0; i < 5; i++ {
				budget.TrackToolCall()
			}
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !res.BudgetPaused && !res.Paused {
			t.Fatal("budget overrun did not pause the loop")
		}
	})

	t.Run("policy_bypass_level_gate", func(t *testing.T) {
		// At L0 the code stage must NEVER execute even with a step handler
		// that would run it (below-level stages are skipped, not bypassed).
		called := false
		lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L0, Mem: memory.NewMemoryStore(t.TempDir())})
		if err != nil {
			t.Fatalf("NewLoop: %v", err)
		}
		_, err = lp.Run("change", func(stage, intent string, wt *execution.Worktree, res *Result) (string, error) {
			if stage == stageCode {
				called = true
			}
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if called {
			t.Error("code stage executed at L0 — autonomy level bypassed")
		}
	})
}
