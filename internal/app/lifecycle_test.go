package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// newTestTaskService creates a minimal TaskService for lifecycle testing
// without building a full Platform (no index/graph — just registry + store +
// bus).
func newTestTaskService(t *testing.T) (*TaskService, *eventbus.Bus) {
	t.Helper()
	bus := eventbus.New()
	reg := agent.NewRegistry()
	store := agent.NewTaskStore(t.TempDir())
	reg.SetTaskStore(store)
	reg.WithBus(bus)
	return &TaskService{
		registry: reg,
		store:    store,
		bus:      bus,
		arts:     NewArtifactStore(t.TempDir()),
	}, bus
}

// lastEvent returns the most recent event on the bus (all kinds).
func lastEvent(t *testing.T, bus *eventbus.Bus) eventbus.Event {
	t.Helper()
	events := bus.History("")
	if len(events) == 0 {
		t.Fatal("no events published")
	}
	return events[len(events)-1]
}

func payloadAction(ev eventbus.Event) string {
	m, ok := ev.Payload.(map[string]string)
	if !ok {
		return ""
	}
	return m["action"]
}

// TestTaskServiceCancelPublishesEvent verifies Cancel persists the state change
// and publishes a task.updated event.
func TestTaskServiceCancelPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	svc.registry.SubmitTask(tk)

	if err := svc.Cancel(tk.ID, "user requested"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskUpdated {
		t.Fatalf("event kind=%s, want task.updated", last.Kind)
	}
	if last.Subject != tk.ID {
		t.Fatalf("event subject=%q, want %q", last.Subject, tk.ID)
	}
	if payloadAction(last) != "cancel" {
		t.Fatalf("event action=%q, want cancel", payloadAction(last))
	}

	stored, err := svc.store.Get(tk.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.State != domain.TaskCancelled {
		t.Fatalf("stored state=%s, want CANCELLED", stored.State)
	}
}

// TestTaskServiceRetryPublishesEvent verifies Retry reopens a FAILED task and
// publishes a task.updated event.
func TestTaskServiceRetryPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	_ = tk.Fail("boom")
	svc.registry.SubmitTask(tk)

	if _, err := svc.Retry(tk.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskUpdated {
		t.Fatalf("event kind=%s, want task.updated", last.Kind)
	}
	if payloadAction(last) != "retry" {
		t.Fatalf("event action=%q, want retry", payloadAction(last))
	}

	stored, _ := svc.store.Get(tk.ID)
	if stored.State != domain.TaskAnalyzing {
		t.Fatalf("stored state=%s, want ANALYZING", stored.State)
	}
}

// TestTaskServiceResumePublishesEvent verifies Resume unblocks a BLOCKED task
// and publishes a task.updated event.
func TestTaskServiceResumePublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	_ = tk.Transition(domain.TaskPlanning)
	_ = tk.Block("waiting")
	svc.registry.SubmitTask(tk)

	if _, err := svc.Resume(tk.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	last := lastEvent(t, bus)
	if payloadAction(last) != "resume" {
		t.Fatalf("event action=%q, want resume", payloadAction(last))
	}
}

// TestTaskServiceRollbackPublishesEvent verifies Rollback publishes a
// task.updated event and persists ROLLED_BACK.
func TestTaskServiceRollbackPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	for _, s := range []domain.TaskState{
		domain.TaskAnalyzing,
		domain.TaskPlanning,
		domain.TaskWaitingApproval,
		domain.TaskApproved,
		domain.TaskExecuting,
		domain.TaskVerifying,
		domain.TaskReadyForPR,
		domain.TaskPRCreated,
	} {
		_ = tk.Transition(s)
	}
	svc.registry.SubmitTask(tk)

	if err := svc.Rollback(tk.ID, "bad deploy"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	last := lastEvent(t, bus)
	if payloadAction(last) != "rollback" {
		t.Fatalf("event action=%q, want rollback", payloadAction(last))
	}
	stored, _ := svc.store.Get(tk.ID)
	if stored.State != domain.TaskRolledBack {
		t.Fatalf("stored state=%s, want ROLLED_BACK", stored.State)
	}
}

// TestTaskServiceHumanTakeoverPublishesEvent verifies HumanTakeover publishes a
// task.blocked event and persists BLOCKED with the human agent.
func TestTaskServiceHumanTakeoverPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	svc.registry.SubmitTask(tk)

	if err := svc.HumanTakeover(tk.ID, "human-1"); err != nil {
		t.Fatalf("HumanTakeover: %v", err)
	}

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskBlocked {
		t.Fatalf("event kind=%s, want task.blocked", last.Kind)
	}
	stored, _ := svc.store.Get(tk.ID)
	if stored.State != domain.TaskBlocked {
		t.Fatalf("stored state=%s, want BLOCKED", stored.State)
	}
	if stored.AgentID != "human-1" {
		t.Fatalf("stored AgentID=%q, want human-1", stored.AgentID)
	}
}

// TestTaskServiceReturnToAgentPublishesEvent verifies ReturnToAgent resumes a
// human-takeover task and publishes a task.updated event with action
// "return_to_agent".
func TestTaskServiceReturnToAgentPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	svc.registry.SubmitTask(tk)

	if err := svc.HumanTakeover(tk.ID, "human-1"); err != nil {
		t.Fatalf("HumanTakeover: %v", err)
	}

	if err := svc.ReturnToAgent(tk.ID, "bot-2"); err != nil {
		t.Fatalf("ReturnToAgent: %v", err)
	}

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskUpdated {
		t.Fatalf("event kind=%s, want task.updated", last.Kind)
	}
	if got := payloadAction(last); got != "return_to_agent" {
		t.Fatalf("action=%q, want return_to_agent", got)
	}
	stored, _ := svc.store.Get(tk.ID)
	if stored.State != domain.TaskAnalyzing {
		t.Fatalf("stored state=%s, want ANALYZING", stored.State)
	}
	if stored.AgentID != "bot-2" {
		t.Fatalf("stored AgentID=%q, want bot-2", stored.AgentID)
	}
}

// TestTaskServiceTimeoutPublishesEvent verifies Timeout publishes a task.failed
// event.
func TestTaskServiceTimeoutPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	svc.registry.SubmitTask(tk)

	if err := svc.Timeout(tk.ID); err != nil {
		t.Fatalf("Timeout: %v", err)
	}

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskFailed {
		t.Fatalf("event kind=%s, want task.failed", last.Kind)
	}
}

// TestPublishIdempotentAtAppLayer verifies end-to-end: the
// TaskService publishes events with deterministic content-derived IDs, so the
// bus dedups an identical re-publish (a retried producer does not duplicate
// side effects) while a distinct event (different payload) still flows.
func TestPublishIdempotentAtAppLayer(t *testing.T) {
	svc, bus := newTestTaskService(t)

	// Two distinct transitions produce distinct events (different payloads).
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	svc.registry.SubmitTask(tk)
	if err := svc.Timeout(tk.ID); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	events := bus.History("")
	if len(events) < 2 {
		t.Fatalf("expected >=2 events after lifecycle, got %d", len(events))
	}
	// Every event must carry a stable non-empty ID (the bus only dedups on
	// non-empty IDs; empty means no idempotency).
	for _, ev := range events {
		if ev.ID == "" {
			t.Fatalf("event %s has empty ID — idempotency disabled at app layer", ev.Kind)
		}
	}

	// Re-publishing the exact same event (same kind+subject+payload) is a
	// no-op: history length and delivery count do not grow.
	dup := events[len(events)-1]
	before := len(bus.History(""))
	var delivered int
	unsub := bus.Subscribe(dup.Kind, func(e eventbus.Event) { delivered++ })
	bus.Publish(dup) // same content-derived ID as the original
	bus.Publish(dup) // again — must be deduped
	bus.Flush()
	unsub()
	if delivered != 0 {
		t.Fatalf("duplicate event delivered %d times, want 0 (idempotency)", delivered)
	}
	if got := len(bus.History("")); got != before {
		t.Fatalf("history grew on duplicate publish: %d -> %d", before, got)
	}

	// A different payload for the same kind+subject still flows: the producer
	// derives a NEW content-addressed ID for the changed event, so it is not
	// deduped against the original.
	ev2 := events[len(events)-1]
	ev2.Payload = map[string]string{"state": "DIFFERENT"}
	ev2.ID = stableEventID(ev2.Kind, ev2.Subject, ev2.Payload.(map[string]string))
	bus.Publish(ev2)
	bus.Flush()
	if got := len(bus.History("")); got != before+1 {
		t.Fatalf("history after distinct event = %d, want %d", got, before+1)
	}
}
