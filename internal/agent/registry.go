package agent

import (
	"fmt"
	"sort"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// Agent extends domain.Agent with the runtime capabilities registered in the
// [Registry].
type Agent struct {
	domain.Agent
	// Capabilities is the set of tool/action names this agent can perform
	// (e.g. "code", "verify"). Empty means the agent has no declared
	// capabilities; enforcement is left to the governance layer.
	Capabilities []string
}

// Registry tracks agent identities and their submitted tasks, backed by an
// in-memory map plus an optional persisted [TaskStore].
type Registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
	tasks  map[string]*Task // in-memory task registry (never definitionally empty)
	store  *TaskStore       // optional persisted store; nil = in-memory
	bus    *eventbus.Bus    // optional event publisher; nil = no-op
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: map[string]Agent{},
		tasks:  map[string]*Task{},
	}
}

// Register adds an agent. It fails closed: an empty or duplicate ID is rejected.
func (r *Registry) Register(a Agent) error {
	if a.ID == "" {
		return fmt.Errorf("agent: cannot register agent with empty ID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agents[a.ID]; exists {
		return fmt.Errorf("agent: agent %q already registered", a.ID)
	}
	r.agents[a.ID] = a
	return nil
}

// Get retrieves an agent by ID. It reports whether the agent was found.
func (r *Registry) Get(id string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	return a, ok
}

// ByType returns all agents of the given type, sorted by ID.
func (r *Registry) ByType(agentType string) []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Agent
	for _, a := range r.agents {
		if a.Type == agentType {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// All returns all registered agents sorted by ID.
func (r *Registry) All() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// WithBus attaches an optional event bus. When non-nil, the registry publishes
// task.created on Submit. A nil bus is a no-op.
func (r *Registry) WithBus(b *eventbus.Bus) *Registry {
	r.bus = b
	return r
}

// SetTaskStore attaches a persisted backing store. When set, Submit persists
// the task so it is retrievable across sessions.
func (r *Registry) SetTaskStore(s *TaskStore) { r.store = s }

// TaskStore exposes the optional persisted backing store.
func (r *Registry) TaskStore() *TaskStore { return r.store }

// SubmitTask registers a task, assigning an ID when none is set, persisting it
// to the optional store, and publishing task.created on the optional bus. It
// fails closed on a nil task.
// When a persisted store is attached, an empty-ID task is saved first so the
// store assigns a cross-process-unique ID ("t-<max+1>" under its file lock);
// the package-level counter alone would restart at t-1 in every process and
// collide in the shared store file. An explicit ID is kept as-is.
func (r *Registry) SubmitTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("agent: cannot submit a nil task")
	}
	persisted := false
	if task.ID == "" {
		if r.store != nil {
			// Store assigns the ID and persists in one locked critical
			// section; skip the second save below.
			saved, err := r.store.Save(*task)
			if err != nil {
				return fmt.Errorf("agent: persist task: %w", err)
			}
			task.ID = saved.ID
			persisted = true
		} else {
			task.ID = nextTaskID()
		}
	}
	r.mu.Lock()
	if _, exists := r.tasks[task.ID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("agent: task %q already submitted", task.ID)
	}
	// Store the pointer so state mutations made by the workflow engine are
	// visible through the registry.
	r.tasks[task.ID] = task
	r.mu.Unlock()
	if r.store != nil && !persisted {
		if _, err := r.store.Save(*task); err != nil {
			return fmt.Errorf("agent: persist task: %w", err)
		}
	}
	if r.bus != nil {
		r.bus.Publish(eventbus.Event{
			Kind:    eventbus.TaskCreated,
			Source:  "agent",
			Subject: task.ID,
			Payload: map[string]string{"type": task.Type, "input": task.Input},
		})
	}
	return nil
}

// GetTask returns a task by ID. It reports whether the task was found. The
// returned pointer aliases the stored task, so its state reflects workflow
// mutations.
func (r *Registry) GetTask(id string) (*Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	return t, ok
}

// ListTasks returns all submitted tasks sorted by ID for determinism.
func (r *Registry) ListTasks() []*Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TaskCount returns the number of submitted tasks.
func (r *Registry) TaskCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tasks)
}
