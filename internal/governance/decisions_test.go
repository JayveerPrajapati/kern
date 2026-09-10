package governance

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestRecordDecisionAudit(t *testing.T) {
	l := NewAuditLog()
	RecordDecision(l, nil, "agent", "egress", "connect", PolicyDecision{Policy: "egress", Decision: "denied", Reason: "external target"}, domain.Risk{Level: domain.RiskLow, Blocked: true})
	entries := l.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Result != "denied" {
		t.Errorf("Result = %q, want denied", e.Result)
	}
	if e.Policy != "egress" {
		t.Errorf("Policy = %q, want egress", e.Policy)
	}
	if e.Reason != "external target" {
		t.Errorf("Reason = %q, want external target", e.Reason)
	}
}

func TestRecordDecisionEvent(t *testing.T) {
	bus := eventbus.New()
	got := make(chan eventbus.Event, 4)
	bus.Subscribe("", func(ev eventbus.Event) { got <- ev })

	l := NewAuditLog()
	RecordDecision(l, bus, "agent", "egress", "connect", PolicyDecision{Policy: "egress", Decision: "allowed", Reason: "local target"}, domain.Risk{Level: domain.RiskLow})
	select {
	case ev := <-got:
		if ev.Kind != eventbus.PolicyEvaluated {
			t.Errorf("kind = %s, want %s", ev.Kind, eventbus.PolicyEvaluated)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PolicyEvaluated event received")
	}

	RecordDecision(l, bus, "agent", "egress", "connect", PolicyDecision{Policy: "egress", Decision: "denied", Reason: "blocked"}, domain.Risk{Level: domain.RiskLow, Blocked: true})
	select {
	case ev := <-got:
		if ev.Kind != eventbus.PolicyBlocked {
			t.Errorf("kind = %s, want %s", ev.Kind, eventbus.PolicyBlocked)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PolicyBlocked event received")
	}
}

func TestRecordDecisionNilBus(t *testing.T) {
	l := NewAuditLog()
	RecordDecision(l, nil, "agent", "egress", "connect", PolicyDecision{Policy: "egress", Decision: "allowed", Reason: "local target"}, domain.Risk{Level: domain.RiskLow})
	if got := len(l.All()); got != 1 {
		t.Errorf("entry should still be recorded with a nil bus, Len = %d", got)
	}
}
