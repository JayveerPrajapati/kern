package loop

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestLoopPausesOnRiskExceeded verifies the Phase 20.4 high-risk escalation gate:
// when the assessed risk of the intent exceeds the configured MaxRiskLevel ceiling,
// the loop PAUSES with reason "risk_exceeded" before running any stages.
func TestLoopPausesOnRiskExceeded(t *testing.T) {
	lp, err := NewLoop(LoopConfig{
		Root:         loopFixture(t),
		Level:        L4,
		MaxRiskLevel: domain.RiskHigh,
		AssessRisk:   func(intent string) domain.RiskLevel { return domain.RiskCritical },
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("risky intent", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Paused {
		t.Fatal("expected the loop to PAUSE when risk exceeds the ceiling")
	}
	if res.PauseReason != "risk_exceeded" {
		t.Fatalf("PauseReason = %q, want risk_exceeded", res.PauseReason)
	}
	if len(res.Stages) != 0 {
		t.Fatalf("expected NO stages to run on risk pause, got %d", len(res.Stages))
	}
}

// TestLoopRiskCeilingRespectsAssessorNil verifies that without an AssessRisk the
// risk gate never trips (preserves prior behavior).
func TestLoopRiskCeilingRespectsAssessorNil(t *testing.T) {
	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L4, Mem: memory.NewMemoryStore(t.TempDir())})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Paused {
		t.Fatalf("loop must not pause without an AssessRisk, got reason %q", res.PauseReason)
	}
}

// TestBudgetPauseSetsPausedFlag verifies the budget-exceed pause also surfaces the
// unified Paused/PauseReason flags (back-compat keeps BudgetPaused true).
func TestBudgetPauseSetsPausedFlag(t *testing.T) {
	budget := &domain.SafetyBudget{MaxToolCalls: 1}
	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Budget: budget})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.BudgetPaused || !res.Paused {
		t.Fatalf("expected BudgetPaused and Paused both true, got %v/%v", res.BudgetPaused, res.Paused)
	}
	if res.PauseReason != "budget" {
		t.Fatalf("PauseReason = %q, want budget", res.PauseReason)
	}
}

// TestPauseOnBudgetEnvNotAllowed verifies the env dimension from Phase 20.3 trips
// the budget and pauses with the env reason.
func TestPauseOnBudgetEnvNotAllowed(t *testing.T) {
	budget := &domain.SafetyBudget{MaxToolCalls: 100, AllowedEnvs: []string{"development"}}
	budget.TrackEnv("production")
	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Budget: budget})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("deploy to prod", func(_ string, _ string, _ *execution.Worktree, _ *Result) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Paused {
		t.Fatal("expected a pause when the current env is not allowed")
	}
	if !strings.Contains(res.PauseReason, "budget") {
		t.Fatalf("expected budget pause, got reason %q", res.PauseReason)
	}
}

// TestPauseOnToolKindLimit verifies per-tool-kind budget caps trigger a pause.
func TestPauseOnToolKindLimit(t *testing.T) {
	budget := &domain.SafetyBudget{
		MaxToolCalls:       100,
		MaxToolCallsByKind: map[string]int{"exec": 1},
	}
	budget.TrackToolCallKind("exec") // reaches the per-kind limit of 1
	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Budget: budget})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("add a helper", func(_ string, _ string, _ *execution.Worktree, _ *Result) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Paused {
		t.Fatal("expected a pause when a per-kind limit is exceeded")
	}
	if !strings.Contains(res.PauseReason, "budget") {
		t.Fatalf("expected budget pause reason, got %q", res.PauseReason)
	}
}

// TestLearnIsAutonomyAware verifies Phase 20.5: the learn stage records the
// autonomy score and a "score" tag in memory.
func TestLearnIsAutonomyAware(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	lp, err := NewLoop(LoopConfig{Root: t.TempDir(), Level: L4, Mem: store, MaxRiskLevel: domain.RiskLow})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if _, err := lp.learn("greet health", &Result{ObservedHealthy: true, Deployed: true}); err != nil {
		t.Fatalf("learn: %v", err)
	}
	ms, _ := store.List("")
	var scored bool
	for _, m := range ms {
		if m.Type == domain.MemoryLesson && !strings.Contains(m.Content, "score ") {
			t.Fatalf("lesson content missing autonomy score: %q", m.Content)
		}
		for _, tag := range m.Tags {
			if tag == "score" {
				scored = true
			}
		}
	}
	if !scored {
		t.Fatal("expected a 'score' tag on learned memory")
	}
}
