// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"log"
)

// PendingApprovals returns the approvals awaiting a human decision, read from
// the same persistent store the workflow/deploy gates write. It makes Approval
// a first-class application service so interfaces never construct the file
// store themselves.
func (s *TaskService) PendingApprovals() ([]domain.Approval, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("task service: platform not configured")
	}
	return governance.NewFileStore(s.platform.Root()).Pending()
}

// ResolveApproval records a human decision on a pending approval. approve=true
// approves, false rejects. The decision is persisted to the shared store so a
// fresh process (or a resumed engine) observes it, and the gated task (when it
// is parked in WAITING_FOR_APPROVAL) is advanced to its approval-resolved
// state so `kern task` reflects the decision immediately.
func (s *TaskService) ResolveApproval(id, approver string, approve bool, reason string) (domain.Approval, error) {
	return s.ResolveApprovalForTask(id, approver, approve, reason)
}

// ResolveApprovalForTask records a human decision on a pending approval and,
// when the approval gates a task parked in WAITING_FOR_APPROVAL, advances that
// task to its approval-resolved state (APPROVED on approve, REJECTED on
// reject) and persists it. Advancing the task state here — instead of only on
// the next workflow resume — makes the decision visible through `kern task`
// and records the gate-crossing transition in the audit chain. The advance is
// best-effort: a task in any other state (already running, blocked, resolved
// out-of-band) is left untouched and the workflow engine still handles it on
// resume. The decision itself is always persisted (fail closed on error).
func (s *TaskService) ResolveApprovalForTask(id, approver string, approve bool, reason string) (domain.Approval, error) {
	if s.platform == nil {
		return domain.Approval{}, fmt.Errorf("task service: platform not configured")
	}
	a, err := governance.NewFileStore(s.platform.Root()).Decide(id, approver, approve, reason)
	if err != nil {
		return a, err
	}
	if a.TaskID == "" {
		return a, nil
	}
	t, ok := s.Get(a.TaskID)
	if !ok || t.State != domain.TaskWaitingApproval {
		return a, nil // not a gated task (or not parked at the gate): nothing to advance
	}
	target := domain.TaskApproved
	if !approve {
		target = domain.TaskRejected
	}
	if err := s.transition(t, target); err != nil {
		// The decision is made; only the task-state advance failed (e.g. an
		// illegal transition). Surface it loudly but keep the approval valid.
		log.Printf("kern app: approval %s decided for task %s but task state could not advance to %s: %v", id, t.ID, target, err)
		return a, nil
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "approval-resolved", "approval": id, "approve": fmt.Sprintf("%t", approve)})
	return a, nil
}
