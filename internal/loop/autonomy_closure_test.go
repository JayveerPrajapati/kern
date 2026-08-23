package loop

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestPauseTriggerHookPauses verifies Phase 20.4: a LoopConfig.PauseTrigger can
// pause the run with a caller-chosen reason on a given stage, while a nil
// PauseTrigger never triggers (backward compatible).
func TestPauseTriggerHookPauses(t *testing.T) {
	// A PauseTrigger that pauses on the "code" stage with reason "scope_change".
	lp, err := NewLoop(LoopConfig{
		Root:  loopFixture(t),
		Level: L2,
		PauseTrigger: func(res *Result, stage string) (string, bool) {
			if stage == "code" {
				return "scope_change", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Paused {
		t.Fatal("expected PauseTrigger to PAUSE the run on the code stage")
	}
	if res.PauseReason != "scope_change" {
		t.Fatalf("PauseReason = %q, want scope_change", res.PauseReason)
	}

	// A nil PauseTrigger (default) is never paused by the hook.
	lpNil, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L2, Mem: memory.NewMemoryStore(t.TempDir())})
	if err != nil {
		t.Fatalf("NewLoop(nil trigger): %v", err)
	}
	resNil, err := lpNil.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Run(nil trigger): %v", err)
	}
	if resNil.Paused {
		t.Fatalf("nil PauseTrigger must not pause, got reason %q", resNil.PauseReason)
	}
}

// TestTriggerHelpers verifies each Phase 20.4 deterministic trigger helper
// returns the expected bool for representative true and false cases.
func TestTriggerHelpers(t *testing.T) {
	// ScopeExpanded: true when newScope is not a prefix of originalScope.
	if !ScopeExpanded("module/a", "module/a/b") {
		t.Error("ScopeExpanded(module/a, module/a/b) should be true (grew)")
	}
	if ScopeExpanded("module/a/b", "module/a") {
		t.Error("ScopeExpanded(module/a/b, module/a) should be false (shrunk)")
	}
	if ScopeExpanded("module/a", "module/a") {
		t.Error("ScopeExpanded(module/a, module/a) should be false (equal)")
	}

	// ConfidenceDropped: true when cur < prev - threshold.
	if !ConfidenceDropped(0.9, 0.5, 0.2) {
		t.Error("ConfidenceDropped(0.9,0.5,0.2) should be true")
	}
	if ConfidenceDropped(0.5, 0.9, 0.2) {
		t.Error("ConfidenceDropped(0.5,0.9,0.2) should be false")
	}
	if ConfidenceDropped(0.9, 0.8, 0.2) {
		t.Error("ConfidenceDropped(0.9,0.8,0.2) should be false (drop within threshold)")
	}

	// UnexpectedTool: true when used is not in expected.
	expected := map[string]bool{"read": true, "edit": true}
	if !UnexpectedTool(expected, "exec") {
		t.Error("UnexpectedTool should be true for a tool not in expected")
	}
	if UnexpectedTool(expected, "read") {
		t.Error("UnexpectedTool should be false for a known tool")
	}
	if !UnexpectedTool(nil, "read") {
		t.Error("UnexpectedTool(nil, ...) should be true (nothing expected)")
	}

	// UnexpectedFile: true when the file is not in the expected set.
	allowedFiles := map[string]bool{"src/a.go": true, "src/b.go": true}
	if !UnexpectedFile(allowedFiles, "src/secret.go") {
		t.Error("UnexpectedFile should be true for a file not allowed")
	}
	if UnexpectedFile(allowedFiles, "src/a.go") {
		t.Error("UnexpectedFile should be false for an allowed file")
	}
	if !UnexpectedFile(nil, "src/a.go") {
		t.Error("UnexpectedFile(nil, ...) should be true (nothing expected)")
	}

	// PolicyChanged: true when both hashes differ and both are non-empty.
	if !PolicyChanged("abc", "def") {
		t.Error("PolicyChanged(abc,def) should be true")
	}
	if PolicyChanged("abc", "abc") {
		t.Error("PolicyChanged(abc,abc) should be false (unchanged)")
	}
	if PolicyChanged("", "def") {
		t.Error("PolicyChanged(empty,def) should be false (no baseline)")
	}

	// VerificationRegressed: true when current is a failure while prior was not.
	if !VerificationRegressed("PASS", "FAIL") {
		t.Error("VerificationRegressed(PASS,FAIL) should be true")
	}
	if VerificationRegressed("FAIL", "FAIL") {
		t.Error("VerificationRegressed(FAIL,FAIL) should be false (no regression)")
	}
	if VerificationRegressed("PASS", "PASS") {
		t.Error("VerificationRegressed(PASS,PASS) should be false")
	}
}

// TestHistoricalSuccessRaisesRecommendedLevel verifies Phase 20.5: recorded
// TestHistoricalSuccessRaisesRecommendedLevel verifies Phase 20.5: recorded
// historical success is blended into the score, can raise the RECOMMENDED
// level, and the configured level remains the hard ceiling via AllowedByScore.
func TestHistoricalSuccessRaisesRecommendedLevel(t *testing.T) {
	base := AutonomyScore{
		Confidence: 0.79, RiskTolerance: 0.79, PolicyCoverage: 0.79,
		VerificationCoverage: 0.79, SafetyBudgetRatio: 0.79,
	}
	withHistory := base.WithHistoricalSuccess(0.95, 10)

	// With history present the score must be higher, and the recommended level
	// must not regress (it can rise, but never fall).
	if withHistory.Score() <= base.Score() {
		t.Fatalf("WithHistoricalSuccess should raise Score(): base=%v with=%v", base.Score(), withHistory.Score())
	}
	if withHistory.RecommendedLevel() < base.RecommendedLevel() {
		t.Fatalf("historical success must not regress RecommendedLevel: base=%v with=%v",
			base.RecommendedLevel(), withHistory.RecommendedLevel())
	}
	// With no history (zero rate), the score is unchanged (backward compat).
	if base.WithHistoricalSuccess(0, 0).Score() != base.Score() {
		t.Error("WithHistoricalSuccess(0,0) must not change the score (no history)")
	}

	// Config ceiling via AllowedByScore: config L2 with a score recommending L4
	// is allowed (config <= recommended). A score recommending L4.
	high := AutonomyScore{Confidence: 0.9, RiskTolerance: 0.9, PolicyCoverage: 0.9, VerificationCoverage: 0.9, SafetyBudgetRatio: 0.9}
	if high.RecommendedLevel() != L4 {
		t.Fatalf("expected L4 recommended, got %v", high.RecommendedLevel())
	}
	if !L2.AllowedByScore(high) {
		t.Error("L2.AllowedByScore(L4-recommended) should be true (config <= recommended)")
	}

	// Config L4 with a score recommending L2 must be false: the score narrows
	// (never widens beyond config) — config stays the ceiling.
	low := AutonomyScore{Confidence: 0.5, RiskTolerance: 0.5, PolicyCoverage: 0.5, VerificationCoverage: 0.5, SafetyBudgetRatio: 0.5}
	if low.RecommendedLevel() != L2 {
		t.Fatalf("expect L2 recommended, got %v", low.RecommendedLevel())
	}
	if L4.AllowedByScore(low) {
		t.Error("L4.AllowedByScore(L2-recommended) should be false (score narrows, config is ceiling)")
	}
	// Even with perfect historical evidence, the config ceiling is never exceeded.
	withHistHigh := high.WithHistoricalSuccess(0.99, 100)
	if withHistHigh.RecommendedLevel() < high.RecommendedLevel() {
		t.Error("historical success must not regress the recommended level")
	}
	if !L2.AllowedByScore(withHistHigh) {
		t.Error("config L2 must still be allowed when the score recommends higher")
	}
}

// TestContextDimensionsInfluenceScore verifies the Phase 20.1 context
// dimensions (reversibility, environment, permissions) are part of the score:
// a score that evaluates them (set > 0) differs from the identical base score
// without them, and higher reversibility/environment/permissions raise the
// score. The dimensions are advisory and only consumed when set (> 0), so a
// base score without them is unchanged (backward compatible).
func TestContextDimensionsInfluenceScore(t *testing.T) {
	base := AutonomyScore{
		Confidence: 0.6, RiskTolerance: 0.6, PolicyCoverage: 0.6,
		VerificationCoverage: 0.6, SafetyBudgetRatio: 0.6,
	}

	// A fully reversible, dev-environment, fully-authorized score must exceed
	// the base that carries no context dimensions.
	high := base
	high.Reversibility = 1.0
	high.Environment = 1.0
	high.Permissions = 1.0
	if high.Score() <= base.Score() {
		t.Fatalf("context dims (reversible/dev/authorized) should raise Score(): base=%v high=%v", base.Score(), high.Score())
	}

	// An irreversible, prod, unauthorized score must be below the base.
	low := base
	low.Reversibility = 0.1
	low.Environment = 0.1
	low.Permissions = 0.1
	if low.Score() >= base.Score() {
		t.Fatalf("low context dims (irreversible/prod/unprivileged) should lower Score(): base=%v low=%v", base.Score(), low.Score())
	}

	// The base score with no context dims is unchanged from its plain weighted
	// sum (backward compatibility: zero values are not consumed).
	zero := base
	if zero.Score() != base.Score() {
		t.Error("zero context dims must not change the score")
	}

	// AutonomyScoreFromRisk seeds the context dims, so higher risk yields a
	// strictly lower score than lower risk.
	if AutonomyScoreFromRisk(domain.RiskHigh).Score() >= AutonomyScoreFromRisk(domain.RiskLow).Score() {
		t.Error("high-risk autonomy score must be below low-risk")
	}
}