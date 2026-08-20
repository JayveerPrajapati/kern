package agent

import "time"

// Handoff transfers a task from one agent to another.
type Handoff struct {
	TaskID    string
	FromAgent string
	ToAgent   string
	Reason    string
	Timestamp time.Time
}

// HandoffManager tracks task handoffs between agents in memory.
type HandoffManager struct {
	handoffs map[string][]Handoff // taskID -> ordered history
}

// NewHandoffManager creates a handoff manager.
func NewHandoffManager() *HandoffManager {
	return &HandoffManager{handoffs: map[string][]Handoff{}}
}

// Handoff records a transfer, appending it to the task's history.
func (h *HandoffManager) Handoff(taskID, fromAgent, toAgent, reason string) Handoff {
	rec := Handoff{
		TaskID:    taskID,
		FromAgent: fromAgent,
		ToAgent:   toAgent,
		Reason:    reason,
		Timestamp: time.Now(),
	}
	h.handoffs[taskID] = append(h.handoffs[taskID], rec)
	return rec
}

// History returns all handoffs for a task, oldest first.
func (h *HandoffManager) History(taskID string) []Handoff {
	return append([]Handoff{}, h.handoffs[taskID]...)
}

// Last returns the most recent handoff for a task.
func (h *HandoffManager) Last(taskID string) (Handoff, bool) {
	hist := h.handoffs[taskID]
	if len(hist) == 0 {
		return Handoff{}, false
	}
	return hist[len(hist)-1], true
}
