package loop

import (
	"testing"
)

func TestL5ProofGateBlocksWriteStagesWithoutProofs(t *testing.T) {
	// L5 without proofs fails closed for write/act stages.
	if L5.AllowsStageWithProofs(stageCode, nil) {
		t.Error("L5 with nil proofs should NOT allow code")
	}
	if L5.AllowsStageWithProofs(stageDeploy, L5Proofs{}) {
		t.Error("L5 with empty proofs should NOT allow deploy")
	}
	if L5.AllowsStageWithProofs(stageProtect, L5Proofs{}) {
		t.Error("L5 with empty proofs should NOT allow protect")
	}
}

func TestL5ProofGateAllowsReadStagesWithoutProofs(t *testing.T) {
	// L5 always allows read-only stages, even without proofs.
	for _, stage := range []string{stageIntent, stageRemember, stageVerify, stageObserve} {
		if !L5.AllowsStageWithProofs(stage, nil) {
			t.Errorf("L5 should always allow read-only stage %s (nil proofs)", stage)
		}
	}
}

func TestL5ProofGateRequiresAllProofs(t *testing.T) {
	// Missing any one proof → denied.
	reqs := RequiredL5Proofs()
	if len(reqs) != 6 {
		t.Fatalf("expected 6 required proofs, got %d", len(reqs))
	}
	for _, missing := range reqs {
		proofs := L5Proofs{}
		for _, r := range reqs {
			if r != missing {
				proofs[r] = true
			}
		}
		if proofs.Satisfied() {
			t.Errorf("proofs missing %s should not be satisfied", missing)
		}
		if L5.AllowsStageWithProofs(stageCode, proofs) {
			t.Errorf("L5 missing %s should NOT allow code", missing)
		}
	}
}

func TestL5ProofGateAllowsWriteStagesWithAllProofs(t *testing.T) {
	// All proofs true → write stages allowed at L5.
	proofs := L5Proofs{
		ProofPolicy:       true,
		ProofVerification: true,
		ProofRollback:     true,
		ProofMonitoring:   true,
		ProofAudit:        true,
		ProofConfidence:   true,
	}
	if !proofs.Satisfied() {
		t.Fatal("all proofs set should be satisfied")
	}
	for _, stage := range []string{stageCode, stageDeploy, stageProtect} {
		if !L5.AllowsStageWithProofs(stage, proofs) {
			t.Errorf("L5 with all proofs should allow %s", stage)
		}
	}
}

func TestL5ProofGateBelowL5IgnoresProofs(t *testing.T) {
	// Below L5, proofs are irrelevant — standard AllowsStage applies.
	// L4 allows deploy regardless of proofs (nil proofs ok).
	if !L4.AllowsStageWithProofs(stageDeploy, nil) {
		t.Error("L4 should allow deploy without proofs")
	}
	// L2 allows code regardless of proofs.
	if !L2.AllowsStageWithProofs(stageCode, nil) {
		t.Error("L2 should allow code without proofs")
	}
	// L1 should not allow code regardless of proofs.
	if L1.AllowsStageWithProofs(stageCode, L5Proofs{ProofPolicy: true}) {
		t.Error("L1 should not allow code even with proofs")
	}
}
