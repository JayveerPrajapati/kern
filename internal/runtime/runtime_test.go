package runtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// fixture returns a Store seeded with a deterministic production scenario for
// the "checkout" service.
func fixture(t *testing.T) *Store {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	st := NewStore()

	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now.Add(-15 * time.Minute)})
	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "def456", Version: "v1.1.9", DeployedAt: now.Add(-2 * time.Hour)})

	st.AddCommit(Commit{SHA: "abc123", Message: "fix: tighten request validation", Author: "alice", Files: []string{"pkg/validate.go"}, CommittedAt: now.Add(-16 * time.Minute)})
	st.AddCommit(Commit{SHA: "def456", Message: "feat: add checkout endpoint", Author: "bob", Files: []string{"cmd/api.go"}, CommittedAt: now.Add(-2 * time.Hour)})

	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "checkout", Severity: "error", Message: "500: http.ErrBodyNotAllowed", Timestamp: now.Add(-3 * time.Minute), TraceID: "t1"},
		{ID: "e2", Type: EventError, Service: "checkout", Severity: "critical", Message: "panic: nil pointer in validator", Timestamp: now.Add(-1 * time.Minute), TraceID: "t1", Attributes: map[string]string{"file": "pkg/validate.go"}},
		{ID: "e3", Type: EventLog, Service: "checkout", Severity: "error", Message: "validation failed", Timestamp: now.Add(-2 * time.Minute), TraceID: "t1"},
		{ID: "e4", Type: EventTrace, Service: "checkout", Message: "span POST /checkout", Timestamp: now.Add(-2 * time.Minute), TraceID: "t1", SpanID: "s1"},
		{ID: "e5", Type: EventMetric, Service: "checkout", Message: "http.request.duration.p99=200ms", Timestamp: now.Add(-2 * time.Minute)},
		{ID: "e6", Type: EventLog, Service: "payments", Severity: "info", Message: "normal operation", Timestamp: now.Add(-1 * time.Minute)},
	})
	return st
}

func TestStoreQueries(t *testing.T) {
	st := fixture(t)

	if got := len(st.Events("checkout")); got != 5 {
		t.Fatalf("checkout events = %d, want 5", got)
	}
	if evs := st.Events("payments"); len(evs) != 1 || evs[0].Service != "payments" {
		t.Fatalf("payments events missing: %d", len(evs))
	}
	if got := len(st.Deployments("checkout")); got != 2 {
		t.Fatalf("checkout deployments = %d, want 2", got)
	}
	if got := len(st.Commits()); got != 2 {
		t.Fatalf("commits = %d, want 2", got)
	}
}

func TestCorrelate(t *testing.T) {
	st := fixture(t)
	alert := domain.Alert{ID: "al1", Severity: domain.SeverityError, Message: "checkout 500s", Service: "checkout", OccurredAt: time.Now().Truncate(time.Second)}

	corr := NewCorrelator(st, 30*time.Minute).Correlate(alert)
	if corr.AffectedService != "checkout" {
		t.Fatalf("affected service = %q, want checkout", corr.AffectedService)
	}
	if len(corr.Deployments) == 0 {
		t.Fatal("expected a recent deployment")
	}
	if len(corr.ErrorEvents) < 2 {
		t.Fatalf("error events = %d, want >=2", len(corr.ErrorEvents))
	}
	if len(corr.RecentCommits) == 0 {
		t.Fatal("expected a recent commit")
	}
}

func TestCorrelatorInfersServiceFromError(t *testing.T) {
	st := fixture(t)
	alert := domain.Alert{ID: "al2", Severity: domain.SeverityCritical, Message: "500s", OccurredAt: time.Now().Truncate(time.Second)}
	corr := NewCorrelator(st, 30*time.Minute).Correlate(alert)
	if corr.AffectedService != "checkout" {
		t.Fatalf("inferred service = %q, want checkout", corr.AffectedService)
	}
}

func TestParseSnapshotRoundTrip(t *testing.T) {
	st := fixture(t)
	snap := Snapshot{
		Events:      st.Events(""),
		Deployments: st.Deployments(""),
		Commits:     st.Commits(),
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseSnapshot(data)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	if len(got.Events("")) != len(snap.Events) {
		t.Fatalf("event count mismatch: %d vs %d", len(got.Events("")), len(snap.Events))
	}
	if len(got.Deployments("")) != 2 {
		t.Fatalf("deployment count = %d", len(got.Deployments("")))
	}
}
