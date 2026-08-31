package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestApplyConsistencyDowngradesConflictingClaims is the exit gate:
// conflicting system knowledge is NEVER silently collapsed into certainty. Two
// claims from DIFFERENT sources stating DIFFERENT things about the same subject
// must downgrade confidence and attach an explainable CONFLICT report to the
// packet; a consistent packet stays untouched (nil report).
func TestApplyConsistencyDowngradesConflictingClaims(t *testing.T) {
	now := time.Now()
	pkt := &domain.ContextPacket{
		Facts: []domain.Claim{
			{Type: domain.ClaimFact, Statement: "svc is stateless", Scope: "svc", Source: "graph", Confidence: 0.9, Timestamp: now},
			{Type: domain.ClaimInference, Statement: "svc holds session state", Scope: "svc", Source: "memory", Confidence: 0.8, Timestamp: now},
		},
	}

	ApplyConsistency(pkt)

	if pkt.Consistency == nil {
		t.Fatal("conflicting packet must carry a consistency report (exit gate)")
	}
	if pkt.Consistency.Result != domain.ConflictPresent {
		t.Errorf("result = %s, want CONFLICT", pkt.Consistency.Result)
	}
	if len(pkt.Consistency.Conflicts) == 0 {
		t.Error("report carries no conflict entries")
	}
	c := pkt.Consistency.Conflicts[0]
	if c.Explanation == "" {
		t.Error("conflict lacks an explanation (14.4)")
	}
	if c.SourceA != "graph" || c.SourceB != "memory" {
		t.Errorf("conflict sources = %s vs %s, want graph vs memory", c.SourceA, c.SourceB)
	}
	// Confidence must be downgraded (halved), never raised.
	for _, claim := range pkt.Facts {
		if claim.Scope != "svc" {
			continue
		}
		if claim.Confidence >= 0.8 {
			t.Errorf("conflicting claim confidence = %v, want downgraded below both sources", claim.Confidence)
		}
	}
}

// TestApplyConsistencyLeavesConsistentPacketUntouched verifies the NO_CONFLICT
// path: agreeing claims from different sources produce no report and no
// downgrade (nil Consistency = treated as internally consistent).
func TestApplyConsistencyLeavesConsistentPacketUntouched(t *testing.T) {
	now := time.Now()
	pkt := &domain.ContextPacket{
		Facts: []domain.Claim{
			{Type: domain.ClaimFact, Statement: "svc listens on :8080", Scope: "svc", Source: "graph", Confidence: 0.9, Timestamp: now},
			{Type: domain.ClaimFact, Statement: "svc listens on :8080", Scope: "svc", Source: "runtime", Confidence: 0.95, Timestamp: now},
		},
	}

	ApplyConsistency(pkt)

	if pkt.Consistency != nil {
		t.Errorf("consistent packet must stay nil-report, got %+v", pkt.Consistency)
	}
	for _, claim := range pkt.Facts {
		if claim.Confidence != 0.9 && claim.Confidence != 0.95 {
			t.Errorf("consistent claim confidence changed to %v", claim.Confidence)
		}
	}
}
