// Audit trail for memory operations.
//
// Every governed memory operation is recorded as an AuditEvent with a
// timestamp, the acting agent, the operation type, and the outcome. Events
// are kept in a bounded in-memory ring and, when a storage.Store is
// attached, persisted under "memory-audit-<seq>" keys so the trail survives
// restarts. Persistence is best-effort: a failed write degrades the trail to
// in-memory but never fails the memory operation being audited.

package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// Operation is the type of a memory operation recorded in the audit trail.
type Operation string

const (
	// OpAdd records a memory being added.
	OpAdd Operation = "add"
	// OpUpdate records a memory being modified or retired to historical.
	OpUpdate Operation = "update"
	// OpRecall records a memory recall.
	OpRecall Operation = "recall"
	// OpDelete records a memory being deleted.
	OpDelete Operation = "delete"
	// OpClear records the whole store being cleared.
	OpClear Operation = "clear"
	// OpSupersede records a memory supersession.
	OpSupersede Operation = "supersede"
	// OpRetention records a retention-policy sweep (expiry/archive/trim).
	OpRetention Operation = "retention"
)

// AuditEvent is a single audited memory operation.
type AuditEvent struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	AgentID   string            `json:"agent_id"`
	Operation Operation         `json:"operation"`
	MemoryID  string            `json:"memory_id,omitempty"`
	Type      domain.MemoryType `json:"type,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Root      string            `json:"root,omitempty"`
	Allowed   bool              `json:"allowed"`
	Reason    string            `json:"reason,omitempty"`
}

// maxAuditEvents caps the in-memory trail so a long-running process cannot
// grow it without bound. Persisted events are never trimmed.
const maxAuditEvents = 1000

// AuditTrail records memory operations. The in-memory view is bounded; when a
// store is attached every event is persisted too.
type AuditTrail struct {
	mu     sync.Mutex
	store  storage.Store // optional persistence; nil = in-memory only
	seq    int
	events []AuditEvent // newest last
}

// NewAuditTrail creates an in-memory audit trail.
func NewAuditTrail() *AuditTrail { return &AuditTrail{} }

// WithStore attaches a storage.Store for persistence. Every recorded event is
// persisted under a "memory-audit-<seq>" key and replayed into memory on
// attach, so Recent/Filter see history written by prior processes.
func (t *AuditTrail) WithStore(s storage.Store) *AuditTrail {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store = s
	t.replayLocked()
	return t
}

// WithDir attaches a file-backed store rooted at dir (created lazily on the
// first write). Existing events in the directory are replayed into memory.
func (t *AuditTrail) WithDir(dir string) *AuditTrail {
	return t.WithStore(storage.NewLocal(dir))
}

// replayLocked loads persisted events into memory. Must hold t.mu.
func (t *AuditTrail) replayLocked() {
	if t.store == nil {
		return
	}
	entries, err := t.store.List(context.Background())
	if err != nil {
		log.Printf("memory: audit replay failed: %v", err)
		return
	}
	var evs []AuditEvent
	maxSeq := 0
	for _, e := range entries {
		var ev AuditEvent
		if err := json.Unmarshal(e.Value, &ev); err != nil {
			continue // corrupt entry: skip, never fail the trail
		}
		evs = append(evs, ev)
		if id, ok := auditEventSeq(ev.ID); ok && id > maxSeq {
			maxSeq = id
		}
	}
	// Restore write order: the store lists keys lexically ("...-10" before
	// "...-2"), so sort by numeric sequence.
	sort.SliceStable(evs, func(i, j int) bool {
		si, oki := auditEventSeq(evs[i].ID)
		sj, okj := auditEventSeq(evs[j].ID)
		switch {
		case oki && okj:
			return si < sj
		case oki:
			return true
		case okj:
			return false
		default:
			return evs[i].Timestamp.Before(evs[j].Timestamp)
		}
	})
	t.events = append(t.events, evs...)
	if len(t.events) > maxAuditEvents {
		t.events = t.events[len(t.events)-maxAuditEvents:]
	}
	if maxSeq > t.seq {
		t.seq = maxSeq
	}
}

// auditEventSeq parses the numeric suffix of an auto-assigned audit ID
// ("memory-audit-N").
func auditEventSeq(id string) (int, bool) {
	const prefix = "memory-audit-"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	return n, err == nil
}

// Record appends an event to the trail. A zero Timestamp is set to now; a
// zero ID is assigned by sequence. Persistence failures are logged and do not
// fail the record.
func (t *AuditTrail) Record(ev AuditEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.ID == "" {
		t.seq++
		ev.ID = fmt.Sprintf("memory-audit-%d", t.seq)
	}

	t.events = append(t.events, ev)
	if len(t.events) > maxAuditEvents {
		t.events = t.events[len(t.events)-maxAuditEvents:]
	}

	if t.store != nil {
		raw, err := json.Marshal(ev)
		if err != nil {
			log.Printf("memory: audit marshal failed: %v", err)
			return nil // never fail the operation being audited
		}
		if err := t.store.Put(context.Background(), ev.ID, raw); err != nil {
			log.Printf("memory: audit persist failed: %v", err)
		}
	}
	return nil
}

// Recent returns the n most recent events, newest first. n <= 0 returns all.
func (t *AuditTrail) Recent(n int) []AuditEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) == 0 {
		return []AuditEvent{}
	}
	if n <= 0 || n > len(t.events) {
		n = len(t.events)
	}
	out := make([]AuditEvent, 0, n)
	for i := len(t.events) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, t.events[i])
	}
	return out
}

// FilterByAgent returns all recorded events for agentID, newest first.
func (t *AuditTrail) FilterByAgent(agentID string) []AuditEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []AuditEvent
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].AgentID == agentID {
			out = append(out, t.events[i])
		}
	}
	return out
}

// FilterByOperation returns all recorded events of op, newest first.
func (t *AuditTrail) FilterByOperation(op Operation) []AuditEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []AuditEvent
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].Operation == op {
			out = append(out, t.events[i])
		}
	}
	return out
}

// Count returns the number of events currently held in the trail.
func (t *AuditTrail) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.events)
}
