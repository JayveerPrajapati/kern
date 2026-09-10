package evidence

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestDetectConflicts_DifferentScopes(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "svc-a is broken", Scope: "svc-a", Confidence: 0.95},
		{Statement: "svc-b is broken", Scope: "svc-b", Confidence: 0.95},
		{Statement: "svc-c is broken", Scope: "svc-c", Confidence: 0.95},
	}
	if got := DetectConflicts(claims); len(got) != 0 {
		t.Fatalf("got %d conflicts, want 0: %+v", len(got), got)
	}
}

func TestDetectConflicts_SameScopeHighConfidence(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "svc-a is broken", Scope: "svc-a", Confidence: 0.95},
		{Statement: "svc-a is healthy", Scope: "svc-a", Confidence: 0.95},
	}
	got := DetectConflicts(claims)
	if len(got) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(got), got)
	}
	want := "conflicting high-confidence claims for scope svc-a"
	if got[0].Reason != want {
		t.Errorf("reason = %q, want %q", got[0].Reason, want)
	}
	if got[0].ClaimA.Statement != claims[0].Statement || got[0].ClaimB.Statement != claims[1].Statement {
		t.Errorf("conflict pair wrong: %q vs %q", got[0].ClaimA.Statement, got[0].ClaimB.Statement)
	}
}

func TestDetectConflicts_OneMatchingPairAmongThree(t *testing.T) {
	// Only pair (0,1) matches: same scope, both high confidence, different
	// statements. Pair (0,2) and (1,2) differ in scope; no duplicates.
	claims := []domain.Claim{
		{Statement: "svc-a is broken", Scope: "svc-a", Confidence: 0.95},
		{Statement: "svc-a is healthy", Scope: "svc-a", Confidence: 0.95},
		{Statement: "svc-b is degraded", Scope: "svc-b", Confidence: 0.95},
	}
	got := DetectConflicts(claims)
	if len(got) != 1 {
		t.Fatalf("got %d conflicts, want exactly 1: %+v", len(got), got)
	}
	// i < j ordering: ClaimA must be the earlier claim.
	if got[0].ClaimA.Statement != claims[0].Statement || got[0].ClaimB.Statement != claims[1].Statement {
		t.Errorf("pair ordering wrong: %q vs %q", got[0].ClaimA.Statement, got[0].ClaimB.Statement)
	}
}

func TestDetectConflicts_LowConfidenceNoConflict(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "svc-a is broken", Scope: "svc-a", Confidence: 0.5},
		{Statement: "svc-a is healthy", Scope: "svc-a", Confidence: 0.5},
	}
	if got := DetectConflicts(claims); len(got) != 0 {
		t.Fatalf("low-confidence claims must not conflict, got %+v", got)
	}
}

func TestDetectConflicts_SameStatement(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "svc-a is healthy", Scope: "svc-a", Confidence: 0.95},
		{Statement: "svc-a is healthy", Scope: "svc-a", Confidence: 0.95},
	}
	if got := DetectConflicts(claims); len(got) != 0 {
		t.Fatalf("identical statements must not conflict, got %+v", got)
	}
}
