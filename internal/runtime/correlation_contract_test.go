package runtime

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestCorrelationContractNoEvidence verifies a correlation with no runtime
// evidence has an UNKNOWN overall contract (13.2).
func TestCorrelationContractNoEvidence(t *testing.T) {
	c := &Correlation{AffectedService: "svc-a"}
	contract := c.Contract()
	if contract.Overall != domain.CorrelationUnknown {
		t.Errorf("overall = %q, want UNKNOWN (no evidence)", contract.Overall)
	}
	if contract.EvidenceCount != 0 {
		t.Errorf("evidence count = %d, want 0", contract.EvidenceCount)
	}
}

// TestCorrelationContractWithEvidence verifies evidence yields a FACTUAL/INFERRED
// contract with per-link classification.
func TestCorrelationContractWithEvidence(t *testing.T) {
	now := time.Now()
	c := &Correlation{
		AffectedService: "svc-a",
		ErrorEvents:     []Event{{ID: "e1", Type: EventError, Service: "svc-a", Timestamp: now}},
		TraceSpans:      []Event{{ID: "t1", Type: EventTrace, TraceID: "trace-1", Timestamp: now}},
		Deployments:     []domain.Deployment{{Service: "svc-a", Version: "v1"}},
	}
	contract := c.Contract()
	if contract.Overall != domain.CorrelationFactual {
		t.Errorf("overall = %q, want FACTUAL (error evidence present)", contract.Overall)
	}
	hasService := false
	for _, l := range contract.Links {
		if l.Stage == "service" {
			hasService = true
			if l.Confidence != domain.CorrelationFactual {
				t.Errorf("service link confidence = %q, want FACTUAL", l.Confidence)
			}
		}
	}
	if !hasService {
		t.Error("no service link in contract")
	}
}

// TestFingerprintStable verifies the change fingerprint is stable across
// case/whitespace differences and sensitive to the target (13.4).
func TestFingerprintStable(t *testing.T) {
	a := Fingerprint("remove_symbol", "UserService", "")
	b := Fingerprint("remove_symbol", "userservice", "")
	if a.Hash != b.Hash {
		t.Errorf("fingerprints differ on case: %s vs %s (13.4)", a.Hash, b.Hash)
	}
	c := Fingerprint("remove_symbol", "PaymentService", "")
	if a.Hash == c.Hash {
		t.Error("different targets must fingerprint differently")
	}
	if a.Canonical == "" || a.Hash == "" {
		t.Error("fingerprint fields not populated")
	}
}

// TestTraceEventsFromCorrelation verifies trace/event evidence links are
// extracted (13.1).
func TestTraceEventsFromCorrelation(t *testing.T) {
	now := time.Now()
	c := &Correlation{
		TraceSpans:  []Event{{ID: "t1", Type: EventTrace, TraceID: "tr-1", Timestamp: now}},
		ErrorEvents: []Event{{ID: "e1", Type: EventError, TraceID: "tr-1", Timestamp: now}},
	}
	links := TraceEventsFromCorrelation(c)
	if len(links) != 2 {
		t.Fatalf("trace links = %d, want 2", len(links))
	}
	if links[0].TraceID != "tr-1" || links[0].Stage != "trace" {
		t.Errorf("trace link = %+v", links[0])
	}
	if links[1].Stage != "error" {
		t.Errorf("error link stage = %q, want error", links[1].Stage)
	}
}

// TestCorrelateChainCarriesTraceLinks verifies CorrelateChain now attaches the
// trace/event evidence links (13.1) so the chain is traceable to raw telemetry.
func TestCorrelateChainCarriesTraceLinks(t *testing.T) {
	now := time.Now()
	st := NewStore()
	st.IngestAll([]Event{
		{ID: "t1", Type: EventTrace, Service: "svc-a", TraceID: "tr-9", Timestamp: now.Add(-time.Minute)},
		{ID: "e1", Type: EventError, Service: "svc-a", Severity: "critical", Message: "boom", TraceID: "tr-9", Timestamp: now.Add(-time.Minute)},
	})
	c := NewCorrelator(st, 30*time.Minute)
	chain := c.CorrelateChain(domain.Alert{ID: "al-1", Service: "svc-a", Severity: domain.SeverityCritical, OccurredAt: now})
	if len(chain.TraceLinks) == 0 {
		t.Error("chain should carry trace links (13.1)")
	}
}

// TestFingerprintFullDimensions verifies the change fingerprint now enumerates
// the full dimension set (files, symbols, services, APIs, database, events,
// risk, tests, agent, model, task) and that a difference on any single
// dimension changes the hash (13.4).
func TestFingerprintFullDimensions(t *testing.T) {
	base := FingerprintChange(ChangeFingerprint{
		Kind: "remove_symbol", Target: "UserService",
		Files:    []string{"user_service.go"},
		Symbols:  []string{"UserService.Delete"},
		Services: []string{"user"},
		APIs:     []string{"DELETE /users/{id}"},
		Database: []string{"users"},
		Events:   []string{"UserDeleted"},
		Risk:     []string{"high"},
		Tests:    []string{"user_delete_test.go"},
		Agent:    []string{"agent-x"},
		Model:    []string{"deepseek"},
		Task:     []string{"TASK-42"},
	})

	// A change identical except for a single extra symbol must differ.
	withExtra := base
	withExtra.Symbols = append(append([]string{}, base.Symbols...), "UserService.Archive")
	withExtra = FingerprintChange(withExtra)
	if withExtra.Hash == base.Hash {
		t.Error("adding a symbol must change the fingerprint hash (13.4)")
	}

	// Ordering of a dimension must not affect the hash (canonicalization sorts).
	reordered := FingerprintChange(ChangeFingerprint{
		Kind: "remove_symbol", Target: "UserService",
		Services: []string{"billing", "checkout"}, Task: []string{"TASK-9"},
	})
	reorderedSwapped := FingerprintChange(ChangeFingerprint{
		Kind: "remove_symbol", Target: "UserService",
		Services: []string{"checkout", "billing"}, Task: []string{"TASK-9"},
	})
	if reordered.Hash != reorderedSwapped.Hash {
		t.Error("dimension ordering must not affect the fingerprint (sorted canonical form)")
	}

	// Missing a dimension (e.g. no risk) must differ from having it.
	withRisk := FingerprintChange(ChangeFingerprint{Kind: "remove_symbol", Target: "UserService", Risk: []string{"high"}})
	withoutRisk := FingerprintChange(ChangeFingerprint{Kind: "remove_symbol", Target: "UserService"})
	if withRisk.Hash == withoutRisk.Hash {
		t.Error("risk dimension must participate in the fingerprint")
	}
}

// TestSharedCorrelatorIdentity verifies that the incident and deployment
// consumers receive the SAME underlying *Correlator via dependency injection,
// so they observe identical correlations .
func TestSharedCorrelatorIdentity(t *testing.T) {
	now := time.Now()
	st := NewStore()
	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "svc-a", Severity: "error", Message: "boom", Timestamp: now.Add(-time.Minute)},
	})

	shared := NewSharedCorrelator(st, 30*time.Minute)

	// incident and deployment each receive the shared instance through DI.
	incident := &incidentConsumer{corr: shared.Correlator()}
	deployment := &deploymentConsumer{corr: shared.Correlator()}

	if incident.corr != deployment.corr {
		t.Fatal("incident and deployment must share the SAME *Correlator instance")
	}

	alert := domain.Alert{ID: "al-1", Service: "svc-a", Severity: domain.SeverityError, OccurredAt: now}
	if got := incident.corr.Correlate(alert); got.AffectedService != "svc-a" {
		t.Errorf("incident correlation service = %q, want svc-a", got.AffectedService)
	}
	if got := deployment.corr.Correlate(alert); got.AffectedService != "svc-a" {
		t.Errorf("deployment correlation service = %q, want svc-a", got.AffectedService)
	}
}

// incidentConsumer is a stand-in for the incident lane that consumes a
// *Correlator via dependency injection.
type incidentConsumer struct{ corr *Correlator }

// deploymentConsumer is a stand-in for the deployment lane that consumes a
// *Correlator via dependency injection.
type deploymentConsumer struct{ corr *Correlator }

// TestDefaultSharedCorrelatorSingleton verifies DefaultSharedCorrelator returns
// the same instance on every call (process-wide shared accessor).
func TestDefaultSharedCorrelatorSingleton(t *testing.T) {
	st := NewStore()
	a := DefaultSharedCorrelator(st, 30*time.Minute)
	b := DefaultSharedCorrelator(st, 30*time.Minute)
	if a != b {
		t.Fatal("DefaultSharedCorrelator must return the same instance")
	}
}
