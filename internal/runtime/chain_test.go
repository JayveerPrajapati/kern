package runtime

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestCorrelateChain(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	st := NewStore()

	st.AddDeployment(domain.Deployment{Service: "checkout", CommitSHA: "abc123", Version: "v1.2.0", DeployedAt: now.Add(-10 * time.Minute)})

	st.AddCommit(Commit{SHA: "abc123", Message: "fix checkout (#42)", Author: "alice", Files: []string{"pkg/checkout.go"}, CommittedAt: now.Add(-9 * time.Minute)})

	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "checkout", Severity: "error", Message: "panic", Timestamp: now.Add(-3 * time.Minute),
			Attributes: map[string]string{"symbol": "Checkout.Run", "file": "pkg/checkout.go", "task": "TASK-7", "agent": "agent-a"}},
	})

	alert := domain.Alert{ID: "al1", Severity: domain.SeverityError, Message: "checkout 500s", Service: "checkout", OccurredAt: now}
	chain := NewCorrelator(st, 30*time.Minute).CorrelateChain(alert)

	if chain.Service != "checkout" {
		t.Fatalf("service = %q, want checkout", chain.Service)
	}

	byStage := map[string]map[string]bool{}
	for _, l := range chain.Links {
		if byStage[l.Stage] == nil {
			byStage[l.Stage] = map[string]bool{}
		}
		byStage[l.Stage][l.ID] = true
	}

	expect := map[string]string{
		"service":    "checkout",
		"deployment": "v1.2.0",
		"commit":     "abc123",
		"symbol":     "Checkout.Run",
		"task":       "TASK-7",
		"agent":      "agent-a",
		"pr":         "42",
	}
	for stage, id := range expect {
		if !byStage[stage][id] {
			t.Fatalf("expected %s link %q, got links %+v", stage, id, chain.Links)
		}
	}

	// file attribute should also appear as a symbol link.
	if !byStage["symbol"]["pkg/checkout.go"] {
		t.Fatalf("expected file-backed symbol link, got %+v", chain.Links)
	}
}

func TestCorrelateChainDedupesLinks(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	st := NewStore()
	st.IngestAll([]Event{
		{ID: "e1", Type: EventError, Service: "svc", Severity: "error", Message: "a", Timestamp: now.Add(-1 * time.Minute),
			Attributes: map[string]string{"symbol": "Sym.A", "task": "T1", "agent": "a1"}},
		{ID: "e2", Type: EventError, Service: "svc", Severity: "error", Message: "b", Timestamp: now.Add(-1 * time.Minute),
			Attributes: map[string]string{"symbol": "Sym.A", "task": "T1", "agent": "a1"}},
	})
	alert := domain.Alert{ID: "al1", Service: "svc", OccurredAt: now}
	chain := NewCorrelator(st, 30*time.Minute).CorrelateChain(alert)

	count := map[string]int{}
	for _, l := range chain.Links {
		count[l.Stage]++
	}
	if count["symbol"] != 1 {
		t.Fatalf("symbol links = %d, want 1 (deduped): %+v", count["symbol"], chain.Links)
	}
	if count["task"] != 1 {
		t.Fatalf("task links = %d, want 1 (deduped)", count["task"])
	}
	if count["agent"] != 1 {
		t.Fatalf("agent links = %d, want 1 (deduped)", count["agent"])
	}
}

func TestCorrelateChainEmptyService(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	st := NewStore()
	alert := domain.Alert{ID: "al3", OccurredAt: now}
	chain := NewCorrelator(st, 30*time.Minute).CorrelateChain(alert)
	if chain.Service != "" {
		t.Fatalf("service = %q, want empty", chain.Service)
	}
	for _, l := range chain.Links {
		if l.Stage == "service" {
			t.Fatalf("should not emit a service link when service is empty: %+v", chain.Links)
		}
	}
}
