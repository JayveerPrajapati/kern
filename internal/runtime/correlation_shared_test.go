package runtime

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestContractCarriesSourceRelationshipTimestampProvenance verifies the Phase
// 13.2 contract metadata (source, relationship, timestamp, provenance) is
// populated on a real correlation built through the store.
func TestContractCarriesSourceRelationshipTimestampProvenance(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	st := NewStore()
	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now.Add(-15 * time.Minute)})
	st.AddCommit(Commit{SHA: "abc123", Message: "fix: validation", CommittedAt: now.Add(-16 * time.Minute)})
	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "checkout", Severity: "critical", Message: "boom", Timestamp: now.Add(-time.Minute)},
	})

	c := NewCorrelator(st, 30*time.Minute).Correlate(domain.Alert{ID: "al-1", Service: "checkout", Severity: domain.SeverityCritical, OccurredAt: now})
	contract := c.Contract()

	if contract.Source != "runtime" {
		t.Errorf("source = %q, want runtime", contract.Source)
	}
	if contract.Relationship == "" {
		t.Error("Relationship is empty, want a CorrelationConfidence classification")
	}
	if contract.Relationship != contract.Overall {
		t.Errorf("Relationship = %q, want to equal Overall %q", contract.Relationship, contract.Overall)
	}
	if contract.Timestamp.IsZero() {
		t.Error("Timestamp is zero, want the computation time")
	}
	if contract.Provenance != "runtime:correlate" {
		t.Errorf("Provenance = %q, want runtime:correlate", contract.Provenance)
	}
}

// TestSharedCorrelatorIsShared verifies NewSharedCorrelator returns a stable
// underlying *Correlator and that two consumers handed the same SharedCorrelator
// share one instance (Phase 13.3).
func TestSharedCorrelatorIsShared(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	st := NewStore()
	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "svc-a", Severity: "critical", Message: "boom", Timestamp: now.Add(-time.Minute)},
	})

	shared := NewSharedCorrelator(st, 30*time.Minute)

	// The underlying *Correlator is stable across accessor calls.
	if a, b := shared.Correlator(), shared.Correlator(); a != b {
		t.Fatal("Correlator() must return the same instance on every call")
	}

	// Two DI consumers share the exact same underlying correlator.
	a := &struct{ corr *Correlator }{shared.Correlator()}
	b := &struct{ corr *Correlator }{shared.Correlator()}
	if a.corr != b.corr {
		t.Fatal("two consumers of one SharedCorrelator must share the same *Correlator")
	}

	alert := domain.Alert{ID: "al-1", Service: "svc-a", Severity: domain.SeverityCritical, OccurredAt: now}
	if got := shared.Correlate(alert); got.AffectedService != "svc-a" {
		t.Errorf("shared Correlate service = %q, want svc-a", got.AffectedService)
	}
	chain := shared.CorrelateChain(alert)
	if chain.Service == "" && len(chain.Links) == 0 {
		t.Error("shared CorrelateChain produced no chain")
	}
}