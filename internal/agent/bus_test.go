package agent

import (
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestRegistrySubmitTaskAndGet verifies SubmitTask registers a task and that
// GetTask returns it.
func TestRegistrySubmitTaskAndGet(t *testing.T) {
	r := NewRegistry()
	tk := NewTask("code", "implement X")
	if err := r.SubmitTask(tk); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	got, ok := r.GetTask(tk.ID)
	if !ok {
		t.Fatal("GetTask: task not found")
	}
	if got.ID != tk.ID || got.Type != "code" {
		t.Errorf("GetTask = %+v, want id=%s type=code", got, tk.ID)
	}
	if r.TaskCount() != 1 {
		t.Errorf("TaskCount = %d, want 1", r.TaskCount())
	}
}

// TestTaskStorePersistGet verifies the TaskStore persists and retrieves tasks.
func TestTaskStorePersistGet(t *testing.T) {
	store := NewTaskStore(t.TempDir())
	tk := NewTask("verify", "run tests")
	if _, err := store.Save(*tk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Input != "run tests" {
		t.Errorf("Get input = %q, want run tests", got.Input)
	}
	// List returns the task.
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Errorf("List = %d, want 1 (err %v)", len(list), err)
	}
}

// TestRegistrySubmitTaskPublishesTaskCreated verifies SubmitTask publishes
// task.created when a bus is attached.
func TestRegistrySubmitTaskPublishesTaskCreated(t *testing.T) {
	bus := eventbus.New()
	var mu sync.Mutex
	var created []eventbus.Event
	bus.Subscribe(eventbus.TaskCreated, func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		created = append(created, ev)
	})

	r := NewRegistry().WithBus(bus)
	if err := r.SubmitTask(NewTask("plan", "plan change")); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	bus.Flush()
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 1 {
		t.Fatalf("task.created events = %d, want 1", len(created))
	}
	if created[0].Subject == "" {
		t.Error("task.created event has empty subject (task id)")
	}
}

// TestWorkflowEnginePublishesLifecycleEvents verifies the workflow engine
// publishes approval_requested / approved / completed across the default
// workflow.
func TestWorkflowEnginePublishesLifecycleEvents(t *testing.T) {
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	reg := NewRegistry()
	reg.Register(Agent{Agent: domain.Agent{ID: "planner", Type: "planner"}})
	reg.Register(Agent{Agent: domain.Agent{ID: "coder", Type: "coder"}})
	reg.Register(Agent{Agent: domain.Agent{ID: "reviewer", Type: "reviewer"}})

	eng := NewWorkflowEngine(reg, governance.NewApprovalWorkflow()).WithBus(bus)

	tk := NewTask("code", "do it")
	_, err := eng.Run(tk, func(action string, task *Task) (string, error) { return "ok", nil })
	if err == nil {
		t.Fatal("expected ErrApprovalRequired on first run")
	}
	bus.Flush()
	if kinds[eventbus.TaskApprovalRequested] == 0 {
		t.Error("no task.approval_requested published")
	}

	// Grant the approval and re-run to completion.
	approvalID := ApprovalID(err)
	if err := eng.CompleteApproval(approvalID, "human"); err != nil {
		t.Fatalf("CompleteApproval: %v", err)
	}
	bus.Flush()
	if kinds[eventbus.TaskApproved] == 0 {
		t.Error("no task.approved published after approval")
	}
	if _, err := eng.Run(tk, func(action string, task *Task) (string, error) { return "ok", nil }); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	bus.Flush()
	if kinds[eventbus.TaskCompleted] == 0 {
		t.Error("no task.completed published")
	}
}
