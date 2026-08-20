package verification

import (
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// waitKinds waits until every kind in want has been observed at least once.
// Delivery is asynchronous, so publishers must not assert synchronously.
func waitKinds(t *testing.T, kinds map[eventbus.Kind]int, mu *sync.Mutex, want ...eventbus.Kind) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		all := true
		for _, k := range want {
			if kinds[k] == 0 {
				all = false
				break
			}
		}
		mu.Unlock()
		if all {
			return
		}
		time.Sleep(time.Millisecond)
	}
	for _, k := range want {
		mu.Lock()
		n := kinds[k]
		mu.Unlock()
		if n == 0 {
			t.Errorf("no %s published", k)
		}
	}
}

// TestVerifyPublishesEvents verifies the engine's bus wiring delivers
// verification lifecycle events. It drives the publish path directly (the full
// Verify runs sub-process builds that are too slow/flaky for a unit test).
func TestVerifyPublishesEvents(t *testing.T) {
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	e := NewEngine("/tmp").WithBus(bus)
	res := &VerificationResult{Verdict: VerdictPass, Target: "svc"}

	e.publish(eventbus.VerificationStarted, res)
	e.publish(eventbus.VerificationCompleted, res)

	waitKinds(t, kinds, &mu, eventbus.VerificationStarted, eventbus.VerificationCompleted)

	// A failing verdict should emit verification.failed instead.
	mu.Lock()
	kinds = map[eventbus.Kind]int{}
	mu.Unlock()
	e.publish(eventbus.VerificationFailed, &VerificationResult{Verdict: VerdictFail})
	waitKinds(t, kinds, &mu, eventbus.VerificationFailed)
}

// TestVerifyNilBusIsNoOp confirms the publish helper is a no-op without a bus.
func TestVerifyNilBusIsNoOp(t *testing.T) {
	e := NewEngine("/tmp")
	e.publish(eventbus.VerificationStarted, &VerificationResult{}) // must not panic
}
