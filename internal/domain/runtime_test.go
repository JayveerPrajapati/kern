package domain

import (
	"testing"
	"time"
)

func TestRuntimeEntityDefaults(t *testing.T) {
	now := time.Now()
	a := Alert{ID: "a1", Severity: SeverityCritical, Service: "checkout", OccurredAt: now}
	if a.Severity != SeverityCritical {
		t.Fatalf("severity = %q, want critical", a.Severity)
	}
	if a.ID != "a1" || a.Service != "checkout" {
		t.Fatalf("unexpected alert: %+v", a)
	}

	d := Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now}
	if d.CommitSHA != "abc123" || d.Version != "v1.2.0" {
		t.Fatalf("unexpected deployment: %+v", d)
	}
}

func TestIncidentTransitions(t *testing.T) {
	inc := Incident{
		ID:       "inc-1",
		Title:    "checkout is returning 500s",
		Severity: SeverityError,
		Status:   IncidentOpen,
		Alert:    Alert{ID: "a1", Service: "checkout"},
	}
	if inc.Status != IncidentOpen {
		t.Fatalf("initial status = %q", inc.Status)
	}
	inc.Status = IncidentRootCauseFound
	if inc.Status != IncidentRootCauseFound {
		t.Fatalf("status after transition = %q", inc.Status)
	}
	if inc.AffectedService != "" {
		t.Fatalf("affected service should start empty, got %q", inc.AffectedService)
	}
}

func TestHypothesisAndRootCauseCarryEvidence(t *testing.T) {
	ev := Evidence{Type: EvidenceRuntime, Source: "metrics", Content: "spike observed after deploy v1.2.0"}
	h := Hypothesis{
		Statement:  "the last deploy changed request validation",
		Source:     "deploy",
		Confidence: ClaimInference,
		Score:      0.85,
		Evidence:   []Evidence{ev},
	}
	if len(h.Evidence) != 1 || h.Confidence != ClaimInference {
		t.Fatalf("bad hypothesis: %+v", h)
	}
	rc := RootCause{Summary: "regression in validation", Hypothesis: h, Files: []string{"pkg/validate.go"}, CommitSHA: "abc123"}
	if rc.Hypothesis.Score != 0.85 || len(rc.Files) != 1 || rc.CommitSHA != "abc123" {
		t.Fatalf("bad root cause: %+v", rc)
	}
}
