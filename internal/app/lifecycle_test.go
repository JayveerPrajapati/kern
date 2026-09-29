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

// TestTaskServiceRunRecordsIntentType pins the dogfooding E-LOW fix: a
// workflow task's Type must reflect its compiled intent kind (CODE_CHANGE →
// "code"), not the hardcoded "analyze" that Create always assigned. The task
// record in kern task / team previously mislabeled every workflow as an
// analysis task.
func TestTaskServiceRunRecordsIntentType(t *testing.T) {
	cases := []struct {
		intent string
		want   string
	}{
		{"implement login flow", "code"},
		{"add caching to UserService", "code"},
		{"investigate incident: auth outage", "incident"},
		{"modernize the payment monolith", "modernize"},
		{"security scan for CVEs", "security"},
		{"write tests for the tokenizer", "test"},
		{"deploy the web console", "deploy"},
		{"audit who changed boundaries", "audit"},
	}
	for _, c := range cases {
		s, _ := newTestTaskService(t)
		res, err := s.Run(c.intent)
		if err != nil {
			t.Fatalf("Run(%q): %v", c.intent, err)
		}
		got, ok := s.registry.GetTask(res.TaskID)
		if !ok || got == nil {
			t.Fatalf("Run(%q): task %s not in registry", c.intent, res.TaskID)
		}
		if got.Type != c.want {
			t.Errorf("Run(%q): task type = %q, want %q", c.intent, got.Type, c.want)
		}
	}
}

// TestTaskServiceAnalysisTasksStayAnalyze guards the inverse of the E-LOW fix:
// read-only analysis commands (Analyze/WhatIf/Plan) must STILL create "analyze"
// tasks — Run/RunWorkflow derive the type, but the analysis path must not.
func TestTaskServiceAnalysisTasksStayAnalyze(t *testing.T) {
	s, _ := newTestTaskService(t)
	task, err := s.createAnalysisTask("what-if: remove checkWeather")
	if err != nil {
		t.Fatalf("createAnalysisTask: %v", err)
	}
	if task.Type != "analyze" {
		t.Errorf("analysis task type = %q, want %q", task.Type, "analyze")
	}
}

// TestCreateWorkflowTaskPersistsCorrectTypeAtCreation pins the persist-time
// ordering (Path B): the intent-derived type must reach the persisted store
// record AND the task.created bus event at CREATION time, with NO state
// transition performed after creation. The existing post-Run assertions
// (TestTaskServiceRunRecordsIntentType) cannot catch this — they observe the
// task after the run's first transition, which re-persists the corrected type
// and hides the fact that the creation-time record (and the bus payload) said
// "analyze". A task abandoned in CREATED previously kept the mislabel on disk
// forever (E-LOW residual: kern-do / kern-loop paths).
func TestCreateWorkflowTaskPersistsCorrectTypeAtCreation(t *testing.T) {
	s, bus := newTestTaskService(t)

	task, err := s.createWorkflowTask("implement login flow")
	if err != nil {
		t.Fatalf("createWorkflowTask: %v", err)
	}
	// No transition performed after creation: the task must still be CREATED,
	// so the persisted record is exactly what was written at creation time.
	if task.CurrentState() != domain.TaskCreated {
		t.Fatalf("task state = %q, want %q (no transition should have occurred)", task.CurrentState(), domain.TaskCreated)
	}

	// The PERSISTED store record must carry the intent-derived type "code"
	// (CompileIntent("implement login flow") → CODE_CHANGE → taskTypeForIntent
	// → "code").
	persisted, err := s.store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted records = %d, want 1", len(persisted))
	}
	if persisted[0].Type != "code" {
		t.Errorf("persisted task type = %q, want %q", persisted[0].Type, "code")
	}

	// The task.created bus event must carry the same type in its payload
	// (the registry-level event from SubmitTask carries payload["type"]).
	var busType string
	for _, ev := range bus.History("") {
		if ev.Kind != eventbus.TaskCreated {
			continue
		}
		m, ok := ev.Payload.(map[string]string)
		if !ok {
			continue
		}
		if t, ok := m["type"]; ok {
			busType = t
		}
	}
	if busType != "code" {
		t.Errorf("task.created bus event payload type = %q, want %q", busType, "code")
	}

	// Create on the same service still persists "analyze".
	at, err := s.Create("something")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if at.Type != "analyze" {
		t.Errorf("Create task type = %q, want %q", at.Type, "analyze")
	}
	persisted, err = s.store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(persisted) != 2 {
		t.Fatalf("persisted records = %d, want 2", len(persisted))
	}
	if persisted[1].Type != "analyze" {
		t.Errorf("persisted Create task type = %q, want %q", persisted[1].Type, "analyze")
	}
}

// TestCreateWorkflowTaskDerivesIncidentOpsTypes pins the classifier outcomes
// the incident/ops entry-point switch (task_incident.go / task_ops.go now call
// createWorkflowTask instead of Create) depends on: each intent string must
// persist the derived type at creation time. The "moderniz"/"modernis" stems
// catch modernize/modernization and the British modernise forms;
// extractServiceRe catches "extract <up to 2 words> service(s)"; patchExecRe
// catches "apply/execute [the|this] patch(es)" → CODE_CHANGE → "code".
// "extract learning patterns" stays UNDERSTAND → "analyze" (the regex requires
// service(s)) — a negative pin. If CompileIntent keywords ever change, this
// test tells you exactly which task types would flip.
func TestCreateWorkflowTaskDerivesIncidentOpsTypes(t *testing.T) {
	cases := []struct {
		intent string
		want   string
	}{
		{"remediate incident: db timeout", "incident"},
		{"correlate alert: high cpu", "incident"},
		{"correlate alert (code): panic in router", "incident"},
		{"investigate incident: 5xx spike", "incident"},
		{"modernize phase 1: extract auth", "modernize"},
		{"modernization analysis", "modernize"},
		{"extract learning patterns", "analyze"},
		{"execute patch", "code"},
	}
	for _, c := range cases {
		s, _ := newTestTaskService(t)
		task, err := s.createWorkflowTask(c.intent)
		if err != nil {
			t.Fatalf("createWorkflowTask(%q): %v", c.intent, err)
		}
		if task.CurrentState() != domain.TaskCreated {
			t.Fatalf("createWorkflowTask(%q): state = %q, want %q (no transition after creation)", c.intent, task.CurrentState(), domain.TaskCreated)
		}
		// The PERSISTED record must carry the derived type at creation time.
		persisted, err := s.store.List()
		if err != nil {
			t.Fatalf("createWorkflowTask(%q): store.List: %v", c.intent, err)
		}
		if len(persisted) != 1 {
			t.Fatalf("createWorkflowTask(%q): persisted records = %d, want 1", c.intent, len(persisted))
		}
		if persisted[0].Type != c.want {
			t.Errorf("createWorkflowTask(%q): persisted task type = %q, want %q", c.intent, persisted[0].Type, c.want)
		}
	}
}

// TestTaskServiceCancelPublishesEvent verifies Cancel persists the state change
// and publishes a task.updated event.
func TestTaskServiceCancelPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)

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

// TestTaskServiceFailPublishesEvent verifies the success path of fail(): a
// task that legally transitions to FAILED is persisted as FAILED and a
// task.failed event is published.
func TestTaskServiceFailPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	_ = svc.registry.SubmitTask(tk)

	svc.fail(tk, "boom")

	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskFailed {
		t.Fatalf("event kind=%s, want task.failed", last.Kind)
	}
	stored, err := svc.store.Get(tk.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.State != domain.TaskFailed {
		t.Fatalf("stored state=%s, want FAILED", stored.State)
	}
}

// TestTaskServiceFailNoEventWhenTransitionFails: when a task cannot legally
// transition to FAILED (already terminal — agent.Task.Fail returns an
// invalid-transition error), fail() must NOT publish a misleading task.failed
// event NOR persist the task as FAILED; the task's real state is preserved
// and only the transition failure is logged.
func TestTaskServiceFailNoEventWhenTransitionFails(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	if err := tk.Complete("done"); err != nil { // COMPLETED is terminal: FAILED is unreachable
		t.Fatalf("Complete: %v", err)
	}
	_ = svc.registry.SubmitTask(tk)

	svc.fail(tk, "should never land")

	for _, ev := range bus.History("") {
		if ev.Kind == eventbus.TaskFailed {
			t.Fatalf("task.failed event must NOT be published when the FAILED transition fails (got %s for %s)", ev.Kind, ev.Subject)
		}
	}
	stored, err := svc.store.Get(tk.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored.State != domain.TaskCompleted {
		t.Fatalf("stored state=%s, want COMPLETED (task must not be persisted as FAILED)", stored.State)
	}
}

// TestTaskServiceTimeoutPublishesEvent verifies Timeout publishes a task.failed
// event.
func TestTaskServiceTimeoutPublishesEvent(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	_ = svc.registry.SubmitTask(tk)

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
	_ = svc.registry.SubmitTask(tk)
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
