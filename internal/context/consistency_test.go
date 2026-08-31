package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestCheckConsistencyNoConflicts(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "PaymentService calls RefundService", Source: "GRAPH", Scope: "PaymentService", Confidence: 0.9, Type: domain.ClaimFact},
		{Statement: "PaymentService calls RefundService", Source: "TESTS", Scope: "PaymentService", Confidence: 0.8, Type: domain.ClaimFact},
	}
	report := CheckConsistency(claims)
	if len(report.Conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(report.Conflicts))
	}
}

func TestCheckConsistencyDetectsConflict(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "PaymentService calls RefundService", Source: "GRAPH", Scope: "PaymentService", Confidence: 0.9, Type: domain.ClaimFact},
		{Statement: "RefundService uses legacy endpoint", Source: "RUNTIME", Scope: "PaymentService", Confidence: 0.7, Type: domain.ClaimFact},
	}
	report := CheckConsistency(claims)
	if len(report.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(report.Conflicts))
	}
	c := report.Conflicts[0]
	if c.Subject != "PaymentService" {
		t.Errorf("Subject=%q, want PaymentService", c.Subject)
	}
	if c.SourceA != domain.SourceGraph || c.SourceB != domain.SourceRuntime {
		t.Errorf("sources = %s vs %s, want GRAPH vs RUNTIME", c.SourceA, c.SourceB)
	}
	// Confidence should be downgraded.
	downgraded, ok := report.ConfidenceDowngrades["PaymentService"]
	if !ok {
		t.Fatal("no confidence downgrade for PaymentService")
	}
	if downgraded >= 0.9 {
		t.Errorf("downgraded confidence=%f, should be < 0.9 (original)", downgraded)
	}
}

func TestCheckConsistencySameSourceNoConflict(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "X is true", Source: "GRAPH", Scope: "X", Confidence: 0.9},
		{Statement: "X is false", Source: "GRAPH", Scope: "X", Confidence: 0.8},
	}
	report := CheckConsistency(claims)
	if len(report.Conflicts) != 0 {
		t.Fatalf("same-source claims should not conflict, got %d", len(report.Conflicts))
	}
}

// TestConflictResultEnum verifies the overall ConflictResult enum.
func TestConflictResultEnum(t *testing.T) {
	// Empty claims -> UNKNOWN.
	r := CheckConsistency(nil)
	if r.Classification() != domain.ConflictUnknown {
		t.Errorf("empty report = %q, want UNKNOWN", r.Classification())
	}

	// Conflicting claims -> CONFLICT.
	claims := []domain.Claim{
		{Scope: "svc", Source: "GRAPH", Statement: "svc is healthy", Timestamp: time.Now()},
		{Scope: "svc", Source: "RUNTIME", Statement: "svc is down", Timestamp: time.Now()},
	}
	r2 := CheckConsistency(claims)
	if r2.Classification() != domain.ConflictPresent {
		t.Errorf("conflict report = %q, want CONFLICT", r2.Classification())
	}
}

// TestConflictExplanationPopulated verifies each conflict carries an
// explanation of why it was flagged.
func TestConflictExplanationPopulated(t *testing.T) {
	claims := []domain.Claim{
		{Scope: "svc", Source: "GRAPH", Statement: "x=true", Timestamp: time.Now()},
		{Scope: "svc", Source: "RUNTIME", Statement: "x=false", Timestamp: time.Now()},
	}
	r := CheckConsistency(claims)
	if len(r.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(r.Conflicts))
	}
	if r.Conflicts[0].Explanation == "" {
		t.Error("conflict should have an explanation (14.4)")
	}
}

// TestStaleDetection verifies a conflict where one side is stale is
// attributed to staleness, and an all-stale agreeing group yields STALE.
func TestStaleDetection(t *testing.T) {
	old := time.Now().Add(-10 * 24 * time.Hour) // older than staleness bound
	now := time.Now()
	claims := []domain.Claim{
		{Scope: "svc", Source: "GIT", Statement: "svc health = degraded", Timestamp: old},
		{Scope: "svc", Source: "RUNTIME", Statement: "svc health = healthy", Timestamp: now},
	}
	r := CheckConsistency(claims)
	if len(r.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(r.Conflicts))
	}
	if r.Conflicts[0].StaleSource != "GIT" {
		t.Errorf("stale source = %q, want GIT (14.3)", r.Conflicts[0].StaleSource)
	}
}

// TestAllStaleGroupMarksStale verifies an agreeing group whose evidence is all
// stale classifies as STALE, not CONFLICT.
func TestAllStaleGroupMarksStale(t *testing.T) {
	old := time.Now().Add(-10 * 24 * time.Hour)
	claims := []domain.Claim{
		{Scope: "svc", Source: "GRAPH", Statement: "svc is healthy", Timestamp: old},
		{Scope: "svc", Source: "MEMORY", Statement: "svc is healthy", Timestamp: old},
	}
	r := CheckConsistency(claims)
	if r.Classification() != domain.ConflictStale {
		t.Errorf("all-stale report = %q, want STALE", r.Classification())
	}
	if len(r.StaleSubjects) == 0 {
		t.Error("stale subjects should be recorded (14.3)")
	}
}
