package eventbus

import (
	"sync"
	"testing"
	"time"
)

// waitFor polls cond until it returns true or the timeout elapses. Delivery is
// now asynchronous, so tests must wait for handler invocations.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within timeout")
}

func TestPublishDeliversToMatchingKind(t *testing.T) {
	b := New()

	var mu sync.Mutex
	var got *Event
	unsub := b.Subscribe(IncidentCreated, func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		if got != nil {
			t.Fatalf("handler invoked more than once")
		}
		e := ev
		got = &e
	})
	defer unsub()

	b.Publish(Event{Kind: IncidentCreated, Subject: "inc-42"})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != nil
	})

	mu.Lock()
	defer mu.Unlock()
	if got.Kind != IncidentCreated {
		t.Errorf("got kind %q, want %q", got.Kind, IncidentCreated)
	}
	if got.Subject != "inc-42" {
		t.Errorf("got subject %q, want %q", got.Subject, "inc-42")
	}
}

func TestPublishWildcard(t *testing.T) {
	b := New()

	var mu sync.Mutex
	var kinds []Kind
	unsub := b.Subscribe("", func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds = append(kinds, ev.Kind)
	})
	defer unsub()

	b.Publish(Event{Kind: IncidentCreated})
	b.Publish(Event{Kind: PRCreated})
	b.Publish(Event{Kind: DeploymentStarted})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(kinds) == 3
	})

	mu.Lock()
	defer mu.Unlock()
	if len(kinds) != 3 {
		t.Fatalf("expected 3 events, got %d", len(kinds))
	}
	// Delivery is asynchronous, so ordering is not guaranteed; assert the set.
	want := map[Kind]bool{IncidentCreated: true, PRCreated: true, DeploymentStarted: true}
	for _, k := range kinds {
		if !want[k] {
			t.Errorf("unexpected kind %q delivered", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("missing kinds: %v", want)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()

	var mu sync.Mutex
	count := 0
	unsub := b.Subscribe(IncidentCreated, func(Event) {
		mu.Lock()
		defer mu.Unlock()
		count++
	})

	b.Publish(Event{Kind: IncidentCreated})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 1
	})

	unsub()
	// idempotent unsubscribe
	unsub()

	b.Publish(Event{Kind: IncidentCreated})
	// Give any stray (incorrect) async delivery time to arrive before asserting.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("expected no further delivery after unsubscribe, got %d deliveries", count)
	}
}

func TestHistoryBounded(t *testing.T) {
	b := New()
	if b.max != 100 {
		t.Fatalf("expected default max 100, got %d", b.max)
	}

	for i := 0; i < 250; i++ {
		b.Publish(Event{Kind: IncidentCreated, Subject: "inc-" + itoa(i)})
	}

	hist := b.History("")
	if len(hist) > b.max {
		t.Fatalf("history exceeds max: got %d entries, max %d", len(hist), b.max)
	}
	if len(hist) != b.max {
		t.Fatalf("expected history to hold exactly %d entries, got %d", b.max, len(hist))
	}
	// oldest pruned: first entry should be "inc-150"
	if hist[0].Subject != "inc-150" {
		t.Errorf("oldest retained subject = %q, want %q", hist[0].Subject, "inc-150")
	}
	// newest retained
	if hist[len(hist)-1].Subject != "inc-249" {
		t.Errorf("newest retained subject = %q, want %q", hist[len(hist)-1].Subject, "inc-249")
	}
}

func TestPublishSetsDefaults(t *testing.T) {
	b := New()
	b.Publish(Event{Kind: IncidentCreated, Subject: "inc-1"})

	hist := b.History(IncidentCreated)
	if len(hist) != 1 {
		t.Fatalf("expected 1 event in history, got %d", len(hist))
	}
	ev := hist[0]
	if ev.ID == "" {
		t.Errorf("expected a non-empty auto-generated ID")
	}
	if ev.OccurredAt.IsZero() {
		t.Errorf("expected a non-zero OccurredAt")
	}
}

func TestEventKindsAreDistinct(t *testing.T) {
	kinds := []Kind{
		// pre-existing kinds
		RepositoryDiscovered,
		TaskCreated,
		AgentStateChanged,
		PolicyEvaluated,
		ApprovalRequested,
		ApprovalGranted,
		ApprovalRejected,
		VerificationCompleted,
		PRCreated,
		DeploymentStarted,
		IncidentCreated,
		IncidentResolved,
		LearningRecorded,
		// additional spec kinds
		RepositoryIndexed,
		GraphBuilt,
		ModuleAnalyzed,
		SymbolDiscovered,
		MemoryCreated,
		MemoryRecalled,
		ContextPacketBuilt,
		ImpactComputed,
		RiskCalculated,
		PlanProduced,
		CodeProduced,
		TestRunCompleted,
		SecurityFinding,
		ArchitectureViolation,
		IncidentInvestigated,
		RootCauseDetermined,
		FixProposed,
		FixApproved,
		FixVerified,
		DeploymentCompleted,
		DeploymentRolledBack,
		ObserveHealthy,
		LessonRecorded,
		AuditRecorded,
		// lifecycle events
		TaskStarted,
		TaskUpdated,
		TaskCompleted,
		TaskFailed,
		TaskBlocked,
		TaskRejected,
		TaskCancelled,
		AgentToolCalled,
		AgentHandoff,
		AgentError,
		AgentCompleted,
		AgentFailed,
		PolicyBlocked,
		VerificationStarted,
		VerificationFailed,
		PRUpdated,
		PRMerged,
		PRRejected,
		DeploymentFailed,
		RuntimeAnomaly,
		CodeChanged,
	}

	seen := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		if k == "" {
			t.Errorf("empty Kind constant found")
			continue
		}
		if seen[k] {
			t.Errorf("duplicate Kind constant: %q", k)
		}
		seen[k] = true
	}

	if len(seen) < 55 {
		t.Fatalf("expected at least 55 distinct non-empty Kind constants, got %d", len(seen))
	}
}

func TestEventStructuredFields(t *testing.T) {
	b := New()

	var mu sync.Mutex
	var got Event
	b.Subscribe("", func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		got = ev
	})

	b.Publish(Event{
		Kind:         "test.event",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		TaskID:       "task-1",
		AgentID:      "agent-1",
		Provenance:   "test",
	})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.Kind != ""
	})

	mu.Lock()
	defer mu.Unlock()
	if got.ProjectID != "proj-1" || got.RepositoryID != "repo-1" ||
		got.TaskID != "task-1" || got.AgentID != "agent-1" ||
		got.Provenance != "test" {
		t.Errorf("structured fields not preserved: %+v", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// TestSubscriberPanicDoesNotCrash verifies that a panicking subscriber is
// recovered and does not prevent other subscribers from running (Bug #1).
func TestSubscriberPanicDoesNotCrash(t *testing.T) {
	b := New()

	var mu sync.Mutex
	var ran bool
	b.Subscribe(IncidentCreated, func(Event) {
		panic("boom")
	})
	b.Subscribe(IncidentCreated, func(Event) {
		mu.Lock()
		defer mu.Unlock()
		ran = true
	})

	// This must not crash the process.
	b.Publish(Event{Kind: IncidentCreated})

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ran
	})
}

// TestEventIDsUnique verifies that two events published within the same
// nanosecond receive distinct IDs (Bug: ID collision).
func TestEventIDsUnique(t *testing.T) {
	b := New()

	b.Publish(Event{Kind: IncidentCreated})
	b.Publish(Event{Kind: IncidentCreated})

	hist := b.History("")
	if len(hist) != 2 {
		t.Fatalf("expected 2 events in history, got %d", len(hist))
	}
	if hist[0].ID == "" || hist[0].ID == hist[1].ID {
		t.Fatalf("expected unique event IDs, got %q and %q", hist[0].ID, hist[1].ID)
	}
}

// TestHistoryCapsLargePayload verifies that oversized payloads are withheld
// from history while count remains bounded (Bug #19).
func TestHistoryCapsLargePayload(t *testing.T) {
	b := New()

	big := make([]byte, 2*maxHistoryPayloadSize)
	b.Publish(Event{Kind: IncidentCreated, Subject: "big", Payload: big})
	b.Publish(Event{Kind: IncidentCreated, Subject: "small", Payload: "tiny"})

	hist := b.History("")
	var smallCount, bigCount int
	for _, ev := range hist {
		switch ev.Subject {
		case "small":
			smallCount++
			if ev.Payload != "tiny" {
				t.Errorf("small payload not preserved: %v", ev.Payload)
			}
		case "big":
			bigCount++
			if ev.Payload != nil {
				t.Errorf("large payload should be nil in history, got %v", ev.Payload)
			}
		}
	}
	if smallCount != 1 {
		t.Errorf("expected small event in history, got %d", smallCount)
	}
	if bigCount != 1 {
		t.Errorf("expected large event retained with nil payload, got %d", bigCount)
	}
}
