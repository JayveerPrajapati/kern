package context

import (
	"testing"

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
