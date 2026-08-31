package incident

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestIncidentEngineUsesSharedCorrelator verifies the incident engine uses the
// injected shared correlator and still produces the same evidence
// as the inline path.
func TestIncidentEngineUsesSharedCorrelator(t *testing.T) {
	root := fixtureRoot(t)
	st, mem := incidentFixture(t)
	fw := governance.NewFirewall()

	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Build one shared correlator and inject it into the engine.
	shared := runtime.NewSharedCorrelator(st, DefaultLookback)
	eng.WithSharedCorrelator(shared)

	if eng.shared == nil {
		t.Fatal("WithSharedCorrelator did not set the shared correlator")
	}
	if eng.shared.Correlator() == nil {
		t.Fatal("shared correlator has nil underlying *Correlator")
	}

	alert := domain.Alert{ID: "a1", Severity: domain.SeverityCritical, Message: "checkout is failing", Service: "checkout", OccurredAt: time.Now()}
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)

	if inc.AffectedService != "checkout" {
		t.Fatalf("affected service = %q, want checkout", inc.AffectedService)
	}
	if len(inc.RelatedDeployments) == 0 {
		t.Fatal("no related deployments produced via shared correlator")
	}
	if len(inc.Evidence) == 0 {
		t.Fatal("no evidence produced via shared correlator")
	}
	if inc.Status != domain.IncidentInvestigating {
		t.Fatalf("status = %q, want INVESTIGATING", inc.Status)
	}
}
