package intelligence

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// eventIndex builds a code-only index whose graph carries event/topic-like
// symbols (OrderCreatedEvent, PaymentTopic) plus ordinary symbols that consume
// and produce them:
// Handler -> OrderService
// OrderService -> OrderCreatedEvent (producer of the event)
// ConsumeOrders -> PaymentTopic (consumer of a topic)
// ConsumeOrders -> OrderCreatedEvent
// Utility -> OrderService (unrelated helper)
func eventIndex() *index.Index {
	return &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			sym("func", "Utility", "util.go", 1),
			sym("func", "OrderService", "order/order.go", 1),
			sym("type", "OrderCreatedEvent", "order/events.go", 1),
			sym("type", "PaymentTopic", "pay/topic.go", 1),
			sym("func", "ConsumeOrders", "pay/consume.go", 1),
		},
		Calls: map[string][]string{
			"OrderService":  {"OrderCreatedEvent"},
			"ConsumeOrders": {"PaymentTopic", "OrderCreatedEvent"},
			"Utility":       {"OrderService"},
		},
		Callers: map[string][]string{
			"OrderCreatedEvent": {"OrderService", "ConsumeOrders"},
			"PaymentTopic":      {"ConsumeOrders"},
			"OrderService":      {"Utility"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestWhatEventsAffected(t *testing.T) {
	g := FromIndex(eventIndex())

	// Changing OrderService affects the event it publishes.
	got := g.WhatEventsAffected("OrderService")
	if len(got) != 1 {
		t.Fatalf("WhatEventsAffected(OrderService) = %d nodes, want 1: %v", len(got), ids(got))
	}
	if got[0].ID != "order.OrderCreatedEvent" {
		t.Errorf("affected event = %q, want order.OrderCreatedEvent", got[0].ID)
	}

	// Changing the event itself returns it directly (self event-like).
	got = g.WhatEventsAffected("OrderCreatedEvent")
	if len(got) != 1 || got[0].ID != "order.OrderCreatedEvent" {
		t.Errorf("WhatEventsAffected(OrderCreatedEvent) = %v, want [order.OrderCreatedEvent]", ids(got))
	}

	// Changing an unrelated symbol affects no events and returns empty (non-nil).
	got = g.WhatEventsAffected("Utility")
	if got == nil {
		t.Fatal("WhatEventsAffected returned nil; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("WhatEventsAffected(Utility) = %v, want none", ids(got))
	}
}

func TestWhatEventsAffectedMissingSymbol(t *testing.T) {
	g := FromIndex(eventIndex())
	got := g.WhatEventsAffected("NoSuchSymbol")
	if got == nil {
		t.Fatal("WhatEventsAffected(missing) returned nil; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("WhatEventsAffected(missing) = %v, want none", ids(got))
	}
}

func ids(nodes []domain.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}
