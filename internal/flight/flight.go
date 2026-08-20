// Package flight is the flight recorder for Workflow E (GOVERN AI
// ENGINEERING — "What did our AI agents do, why, and what happened?").
//
// It records a deterministic, append-only log of every agent action: which
// agent performed which task, the tool/operation invoked, its arguments, a
// short context snapshot (e.g. the agent's intent), the outcome, and its
// approval status. Records persist per project through the internal/storage
// Store interface (LocalStore by default). The package is additive,
// deterministic, and stdlib-only.
package flight

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// Record is a single auditable flight-recorder entry describing one agent
// action. Action is the tool/operation name, Context is a short snapshot of
// the surrounding intent, Result is the outcome, and Status is one of
// "ok", "error", "blocked", or "denied".
type Record struct {
	ID        string
	AgentID   string
	TaskID    string
	Action    string
	Arguments string
	Context   string
	Result    string
	Status    string
	Approved  bool
	Timestamp time.Time
}

// ActionType is a typed flight-recorder action name (spec §23). The Action
// field on Record accepts any string for backward compatibility; these
// constants provide the spec's canonical vocabulary so callers use
// consistent names.
type ActionType string

const (
	ActionTaskStarted         ActionType = "task_started"
	ActionContextRetrieved    ActionType = "context_retrieved"
	ActionMemoryRetrieved     ActionType = "memory_retrieved"
	ActionToolCalled          ActionType = "tool_called"
	ActionDecisionMade        ActionType = "decision_made"
	ActionFileModified        ActionType = "file_modified"
	ActionTestExecuted        ActionType = "test_executed"
	ActionGuardrailTriggered  ActionType = "guardrail_triggered"
	ActionApprovalRequested   ActionType = "approval_requested"
	ActionChangeAccepted      ActionType = "change_accepted"
	ActionDeploymentPerformed ActionType = "deployment_performed"
	ActionProductionOutcome   ActionType = "production_outcome"
)

// NewRecord creates a Record with a typed ActionType, defaulting ID and
// timestamp. It is the preferred way to construct a flight record.
func NewRecord(agentID, taskID string, action ActionType) Record {
	return Record{
		AgentID:   agentID,
		TaskID:    taskID,
		Action:    string(action),
		Timestamp: time.Now().UTC(),
	}
}

// bufferCap bounds the in-memory buffer so the recorder never holds an
// unbounded number of records; older records remain persisted on disk.
const bufferCap = 100

// Recorder records agent actions in memory (capped) and persists them through
// a storage.Store.
type Recorder struct {
	store  storage.Store
	mu     sync.Mutex // guards buffer against concurrent Record/List access
	buffer []Record
}

// New returns a Recorder that persists to dir/.kern/flight via a fresh
// LocalStore. Records are kept inside .kern/ so they never pollute the
// project root (and .kern/ is already covered by kern's gitignore block).
func New(dir string) *Recorder {
	return &Recorder{store: storage.NewLocal(filepath.Join(dir, ".kern", "flight"))}
}

// Record appends rec to the in-memory buffer (capped at the most recent 100
// records) and persists it through the store. If rec.ID is empty it is
// generated as "f-<unixnano>"; if rec.Timestamp is zero it is set to the
// current UTC time. It returns the record with those defaults applied.
func (r *Recorder) Record(rec Record) (Record, error) {
	if rec.ID == "" {
		rec.ID = fmt.Sprintf("f-%d", time.Now().UnixNano())
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	r.mu.Lock()
	r.buffer = append(r.buffer, rec)
	if len(r.buffer) > bufferCap {
		r.buffer = r.buffer[len(r.buffer)-bufferCap:]
	}
	r.mu.Unlock()

	raw, err := storage.MarshalValue(&rec)
	if err != nil {
		return rec, err
	}
	if err := r.store.Put(context.Background(), rec.ID, raw); err != nil {
		return rec, err
	}
	return rec, nil
}

// List returns every known record, most-recent-first. It starts from the
// in-memory buffer and hydrates any persisted records not already in the
// buffer by reading the store, merging by ID (dedupe). The result is
// deterministic: newest timestamp first, then by ID.
func (r *Recorder) List() ([]Record, error) {
	r.mu.Lock()
	snapshot := make([]Record, len(r.buffer))
	copy(snapshot, r.buffer)
	r.mu.Unlock()

	byID := make(map[string]Record, len(snapshot))
	for _, rec := range snapshot {
		byID[rec.ID] = rec
	}

	entries, err := r.store.List(context.Background())
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if _, ok := byID[e.Key]; ok {
			continue
		}
		var rec Record
		if err := storage.UnmarshalValue(e.Value, &rec); err != nil {
			// A single corrupt record must not hide the rest of the trail.
			log.Printf("flight: skipping corrupt record %q: %v", e.Key, err)
			continue
		}
		if rec.ID == "" {
			rec.ID = e.Key
		}
		byID[rec.ID] = rec
	}

	out := make([]Record, 0, len(byID))
	for _, rec := range byID {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Timestamp.After(out[j].Timestamp)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Filter returns List() filtered by agentID, taskID, and status. An empty
// value for any filter means "match all". The result inherits List()'s
// deterministic ordering.
func (r *Recorder) Filter(agentID, taskID, status string) []Record {
	records, err := r.List()
	if err != nil {
		log.Printf("flight: Filter failed: %v", err)
		return nil
	}
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		if agentID != "" && rec.AgentID != agentID {
			continue
		}
		if taskID != "" && rec.TaskID != taskID {
			continue
		}
		if status != "" && rec.Status != status {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// query returns every record for taskID whose Action matches one of the given
// action types, in chronological order (oldest first, then by ID for
// determinism). If actions is empty, every record for the task matches.
// It is nil-safe: a nil *Recorder returns nil.
func (r *Recorder) query(taskID string, actions ...ActionType) []Record {
	if r == nil {
		return nil
	}
	records, err := r.List()
	if err != nil {
		log.Printf("flight: query failed: %v", err)
		return nil
	}
	var out []Record
	for _, rec := range records {
		if rec.TaskID != taskID {
			continue
		}
		if len(actions) > 0 {
			match := false
			for _, a := range actions {
				if rec.Action == string(a) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Timestamp.Before(out[j].Timestamp)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// WhyDecision returns all decision_made records for the given task, in
// chronological order. Answers Workflow E: "why did the agent make this
// decision?"
func (r *Recorder) WhyDecision(taskID string) []Record {
	return r.query(taskID, ActionDecisionMade)
}

// WhatContextUsed returns all context_retrieved and memory_retrieved records
// for the task, in chronological order. Answers Workflow E: "what context or
// memory did the agent use?"
func (r *Recorder) WhatContextUsed(taskID string) []Record {
	return r.query(taskID, ActionContextRetrieved, ActionMemoryRetrieved)
}

// WhichToolsCalled returns all tool_called records for the task, in
// chronological order. Answers Workflow E: "which tools did the agent call?"
func (r *Recorder) WhichToolsCalled(taskID string) []Record {
	return r.query(taskID, ActionToolCalled)
}

// WhoApproved returns all approval_requested and change_accepted records for
// the task, in chronological order. Answers Workflow E: "who approved this
// change?"
func (r *Recorder) WhoApproved(taskID string) []Record {
	return r.query(taskID, ActionApprovalRequested, ActionChangeAccepted)
}

// WhatChanged returns all file_modified records for the task, in chronological
// order. Answers Workflow E: "what files were modified?"
func (r *Recorder) WhatChanged(taskID string) []Record {
	return r.query(taskID, ActionFileModified)
}

// WhatTested returns all test_executed records for the task, in chronological
// order. Answers Workflow E: "what tests were run?"
func (r *Recorder) WhatTested(taskID string) []Record {
	return r.query(taskID, ActionTestExecuted)
}

// WhatHappened returns ALL records for the task in chronological order — the
// full audit trail. Answers Workflow E: "what happened end-to-end?"
func (r *Recorder) WhatHappened(taskID string) []Record {
	return r.query(taskID)
}
