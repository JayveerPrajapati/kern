package eventbus

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestHandlerTimeoutAbandonsSlowHandler verifies that a handler exceeding the
// configured timeout is abandoned so Flush returns without waiting for it, and
// the handler goroutine does not hold a delivery slot forever.
func TestHandlerTimeoutAbandonsSlowHandler(t *testing.T) {
	b := New()
	b.SetHandlerTimeout(30 * time.Millisecond)

	var started, finished atomic.Int32
	b.Subscribe(TaskCreated, func(ev Event) {
		started.Add(1)
		time.Sleep(2 * time.Second) // deliberately slower than the timeout
		finished.Add(1)
	})

	b.Publish(Event{Kind: TaskCreated, Subject: "t-1"})
	b.Flush() // must return promptly despite the slow handler

	if started.Load() != 1 {
		t.Fatalf("handler never started")
	}
	if finished.Load() != 0 {
		t.Fatalf("slow handler should have been abandoned before finishing, finished=%d", finished.Load())
	}
}

// TestHandlerTimeoutDisabledRunsToCompletion verifies that the default
// (timeout disabled) behavior is preserved: a handler runs to completion.
func TestHandlerTimeoutDisabledRunsToCompletion(t *testing.T) {
	b := New() // no timeout set

	var count atomic.Int32
	b.Subscribe(TaskCreated, func(ev Event) {
		time.Sleep(20 * time.Millisecond)
		count.Add(1)
	})

	b.Publish(Event{Kind: TaskCreated, Subject: "t-2"})
	b.Flush()

	if got := count.Load(); got != 1 {
		t.Fatalf("handler should have completed once, got %d", got)
	}
}

// TestHandlerTimeoutDeadLetters verifies that a timed-out handler is treated
// as a delivery failure: the event is routed to the dead-letter queue (so a
// slow handler is observable and recoverable) without burning retries — the
// event is abandoned and dead-lettered promptly rather than retried.
func TestHandlerTimeoutDeadLetters(t *testing.T) {
	b := New()
	b.SetHandlerTimeout(20 * time.Millisecond)

	var attempts, dead atomic.Int32
	b.SubscribeDeadLetter(func(ev Event) { dead.Add(1) })
	b.Subscribe(TaskCreated, func(ev Event) {
		attempts.Add(1)
		time.Sleep(2 * time.Second)
	})

	b.Publish(Event{Kind: TaskCreated, Subject: "t-3"})
	b.Flush()

	if got := dead.Load(); got != 1 {
		t.Fatalf("timed-out handler should be dead-lettered once, dead=%d", got)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("timed-out handler should not be retried, attempts=%d", got)
	}
}
