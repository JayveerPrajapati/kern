package governance

import (
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// waitKinds waits until every kind in want has been observed at least once.
// Delivery is asynchronous, so publishers must not assert synchronously.

// TestFirewallPublishesLifecycle verifies the firewall publishes
// policy.evaluated, policy.blocked and approval.requested when a bus is
// attached.
func TestFirewallPublishesLifecycle(t *testing.T) {
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	fw := NewFirewall().WithBus(bus)
	fw.WithAgents(NewAgent("coder", "Coder", "coder", []Permission{
		{Resource: "source", Action: "write"},
		{Resource: "security", Action: "write"},
	}))

	// Allowed action: policy.evaluated, no block.
	if allowed, _, _, err := fw.Check("coder", "source", "write"); err != nil || !allowed {
		t.Fatalf("Check(source.write) = %v, %v", allowed, err)
	}
	waitKinds(t, kinds, &mu, eventbus.PolicyEvaluated)

	// Denied action: policy.blocked.
	if _, _, _, err := fw.Check("coder", "source", "drop"); err == nil {
		t.Fatal("expected error for denied action")
	}
	waitKinds(t, kinds, &mu, eventbus.PolicyBlocked)

	// Approval-required action: approval.requested.
	if allowed, _, appr, _ := fw.Check("coder", "security", "write"); allowed || appr == nil {
		t.Fatalf("expected approval request for security.write; allowed=%v appr=%v", allowed, appr)
	}
	waitKinds(t, kinds, &mu, eventbus.ApprovalRequested)
}

// TestFirewallNilBusIsNoOp confirms the firewall works unchanged without a bus.
func TestFirewallNilBusIsNoOp(t *testing.T) {
	fw := NewFirewall().WithAgents(NewAgent("coder", "Coder", "coder", []Permission{{Resource: "source", Action: "write"}}))
	if allowed, _, _, err := fw.Check("coder", "source", "write"); err != nil || !allowed {
		t.Fatalf("allowed = %v, err = %v", allowed, err)
	}
}
