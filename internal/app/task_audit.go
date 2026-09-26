// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"log"
	"path/filepath"
	"time"
)

// auditTransition writes a task-lifecycle entry into the unified audit chain.
// A nil audit log (no wiring) is a no-op. Entry shape matches the
// governance.AuditEntry API used by firewall writers; the transition is
// recorded as an allowed task-lifecycle event carrying task ID, from → to
// state, agent and timestamp.
func (s *TaskService) auditTransition(t *agent.Task, from, to domain.TaskState) {
	if s.auditLog == nil {
		return // back-compat: no audit log wired = no-op
	}
	entry := governance.AuditEntry{
		AgentID:   s.agentID,
		TaskID:    t.ID,
		Action:    "task.transition",
		Resource:  "task:" + t.ID,
		Result:    "allowed",
		Approved:  true,
		Policy:    "task-lifecycle",
		Reason:    fmt.Sprintf("%s -> %s (agent %s)", from, to, s.agentID),
		Timestamp: time.Now(),
	}
	if err := s.auditLog.AppendExternal(entry); err != nil {
		// Loud, non-blocking: the transition already happened and must not
		// be rolled back because the audit trail could not be written (a
		// missing chain entry is detectable via chain repair; a blocked task
		// is not).
		log.Printf("kern app: task %s transition %s -> %s NOT recorded in audit chain (agent %s): %v", t.ID, from, to, s.agentID, err)
	}
}

// AuditLog returns the shared tamper-evident audit log backing this service. It
// makes Audit a first-class application service so interfaces read the same
// authoritative governance trail the running firewall writes.
func (s *TaskService) AuditLog() *governance.AuditLog {
	if fw := s.Firewall(); fw != nil {
		return fw.AuditLog()
	}
	return nil
}

// AuditEntries reads every persisted audit entry from the shared store under
// <root>/.kern/audit/, the same trail the running firewall writes. The
// in-memory AuditLog().All() only returns entries recorded in THIS process, so
// a fresh CLI/MCP process would otherwise see nothing. Entries whose files are
// missing/corrupt are skipped rather than aborting the listing; order matches
// the store's List order (legacy per-key files by key, then chain entries in
// append order).
func (s *TaskService) AuditEntries() ([]governance.AuditEntry, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("task service: platform not configured")
	}
	store := storage.NewLog(filepath.Join(s.platform.Root(), ".kern", "audit"))
	ctx := context.Background()
	entries, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range entries {
		var entry governance.AuditEntry
		if err := storage.UnmarshalValue(e.Value, &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// AuditEntriesForTask returns only the persisted audit entries whose TaskID
// matches, preserving order. It mirrors the CLI's filterByTask so every
// interface (CLI, MCP) filters through the service.
func (s *TaskService) AuditEntriesForTask(taskID string) ([]governance.AuditEntry, error) {
	entries, err := s.AuditEntries()
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range entries {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	return out, nil
}

// recordArtifact creates a domain.Artifact with the given kind and links it
// into the task's artifact chain via parentID. It persists the artifact to the
// ArtifactStore and appends the artifact ID to the Task's Artifacts slice.
// parentID may be empty (root artifact). provenance records how the artifact
// was produced (e.g. "context:analyze", "whatif:simulate").
func (s *TaskService) recordArtifact(kind domain.ArtifactKind, taskID, createdBy, scope, parentID, provenance string) {
	if s.arts == nil {
		return
	}
	art := domain.NewArtifact(kind, taskID, scope)
	art.CreatedBy = createdBy
	art.Status = "final"
	art.Scope = scope
	art.Provenance = provenance
	art.ParentArtifactID = parentID
	saved, err := s.arts.Save(art)
	if err != nil {
		return
	}
	// Append the artifact ID to the task's Artifacts slice so the chain is
	// reachable from the Task.
	if t, ok := s.registry.GetTask(taskID); ok {
		t.Artifacts = append(t.Artifacts, saved.ID)
	}
}

// lastArtifactID returns the ID of the most recent artifact of the given kind
// for a task, or "" when none exists. It is used to link a new artifact to its
// predecessor in the chain (e.g. ImpactReport → parent ContextPacket).
func (s *TaskService) lastArtifactID(taskID string, kind domain.ArtifactKind) string {
	if s.arts == nil {
		return ""
	}
	arts, err := s.arts.GetByTask(taskID)
	if err != nil {
		return ""
	}
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Kind == kind {
			return arts[i].ID
		}
	}
	return ""
}
