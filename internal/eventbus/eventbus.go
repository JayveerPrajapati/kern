// Package eventbus provides a deterministic, in-process publish/subscribe
// event bus carrying typed system events. It is stdlib-only (sync, time,
// strconv), performs no network I/O, and imports nothing from other internal
// packages; it is intended for fanning system events out to webhooks and the
// audit trail.
package eventbus

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// maxHistoryPayloadSize bounds the serialized size of a payload retained in
// history (Bug #19). Larger payloads are dropped from history to keep the
// bounded-capacity history bounded in bytes as well as count.
const maxHistoryPayloadSize = 4 << 10 // 4 KiB

// Kind discriminates the class of system event crossing the bus .
type Kind string

const (
	RepositoryDiscovered  Kind = "repository.discovered"
	TaskCreated           Kind = "task.created"
	AgentStateChanged     Kind = "agent.state_changed"
	PolicyEvaluated       Kind = "policy.evaluated"
	ApprovalRequested     Kind = "approval.requested"
	ApprovalGranted       Kind = "approval.granted"
	ApprovalRejected      Kind = "approval.rejected"
	VerificationCompleted Kind = "verification.completed"
	PRCreated             Kind = "pr.created"
	DeploymentStarted     Kind = "deployment.started"
	IncidentCreated       Kind = "incident.created"
	IncidentResolved      Kind = "incident.resolved"
	IncidentUpdated       Kind = "incident.updated"
	LearningRecorded      Kind = "learning.recorded"

	// RepositoryIndexed indicates a repository was scanned and indexed.
	RepositoryIndexed Kind = "repository.indexed"
	// GraphBuilt indicates the code knowledge graph was (re)built.
	GraphBuilt Kind = "graph.built"
	// ModuleAnalyzed indicates a module was analyzed.
	ModuleAnalyzed Kind = "module.analyzed"
	// SymbolDiscovered indicates a symbol was discovered in the codebase.
	SymbolDiscovered Kind = "symbol.discovered"
	// MemoryCreated indicates a memory was created in the knowledge store.
	MemoryCreated Kind = "memory.created"
	// MemoryRecalled indicates a memory was recalled from the knowledge store.
	MemoryRecalled Kind = "memory.recalled"
	// MemoryUpdated indicates an existing memory entry was updated/superseded.
	MemoryUpdated Kind = "memory.updated"
	// ContextPacketBuilt indicates a context packet was assembled.
	ContextPacketBuilt Kind = "context_packet.built"
	// ImpactComputed indicates a change's impact was computed.
	ImpactComputed Kind = "impact.computed"
	// RiskCalculated indicates a risk assessment was performed.
	RiskCalculated Kind = "risk.calculated"
	// PlanProduced indicates an execution plan was produced.
	PlanProduced Kind = "plan.produced"
	// CodeProduced indicates code was generated.
	CodeProduced Kind = "code.produced"
	// TestRunCompleted indicates a test run finished.
	TestRunCompleted Kind = "test_run.completed"
	// SecurityFinding indicates a security scan finished.
	SecurityFinding Kind = "security.finding"
	// ArchitectureViolation indicates architecture constraints were violated.
	ArchitectureViolation Kind = "architecture.violation"
	// ArchitectureWarning indicates an architecture check ran but could not be
	// enforced (e.g. no boundaries file configured) — a warning, not a violation.
	ArchitectureWarning Kind = "architecture.warning"
	// IncidentInvestigated indicates an incident investigation began.
	IncidentInvestigated Kind = "incident.investigated"
	// RootCauseDetermined indicates an incident root cause was identified.
	RootCauseDetermined Kind = "root_cause.determined"
	// FixProposed indicates a fix was proposed.
	FixProposed Kind = "fix.proposed"
	// FixApproved indicates a fix was approved.
	FixApproved Kind = "fix.approved"
	// FixVerified indicates a fix was verified.
	FixVerified Kind = "fix.verified"
	// DeploymentCompleted indicates a deployment finished.
	DeploymentCompleted Kind = "deployment.completed"
	// DeploymentRolledBack indicates a deployment was rolled back.
	DeploymentRolledBack Kind = "deployment.rolled_back"
	// LockAcquired indicates a workspace lock was acquired.
	LockAcquired Kind = "lock.acquired"
	// LockContended indicates a workspace lock acquisition failed because
	// another process holds the lock.
	LockContended Kind = "lock.contended"
	// LockReleased indicates a workspace lock was released.
	LockReleased Kind = "lock.released"
	// ObserveHealthy indicates the system was observed healthy.
	ObserveHealthy Kind = "observe.healthy"
	// LessonRecorded indicates a lesson was recorded.
	LessonRecorded Kind = "learning.lesson_recorded"
	// PatternSurfaced indicates the continuous-learning extractor surfaced a
	// recurring pattern and wrote a constraint back to memory.
	PatternSurfaced Kind = "learning.pattern_surfaced"
	// AuditRecorded indicates an audit entry was recorded.
	AuditRecorded Kind = "audit.recorded"

	// TaskStarted indicates a task began executing.
	TaskStarted Kind = "task.started"
	// TaskUpdated indicates a task was updated.
	TaskUpdated Kind = "task.updated"
	// TaskStateChanged indicates a task transitioned between lifecycle states
	// (CREATED → ANALYZING → ... ). It is the canonical state-change event the
	// event taxonomy requires, distinct from generic TaskUpdated.
	TaskStateChanged Kind = "task.state_changed"
	// TaskCompleted indicates a task completed successfully.
	TaskCompleted Kind = "task.completed"
	// TaskFailed indicates a task failed.
	TaskFailed Kind = "task.failed"
	// TaskBlocked indicates a task was blocked.
	TaskBlocked Kind = "task.blocked"
	// TaskRejected indicates a task was rejected.
	TaskRejected Kind = "task.rejected"
	// TaskCancelled indicates a task was cancelled.
	TaskCancelled Kind = "task.cancelled"
	// AgentToolCalled indicates an agent called a tool.
	AgentToolCalled Kind = "agent.tool_called"
	// AgentStarted indicates an agent began executing its task/session.
	AgentStarted Kind = "agent.started"
	// AgentHandoff indicates an agent handed off to another agent.
	AgentHandoff Kind = "agent.handoff"
	// AgentError indicates an agent encountered an error.
	AgentError Kind = "agent.error"
	// AgentCompleted indicates an agent finished its task successfully.
	AgentCompleted Kind = "agent.completed"
	// AgentFailed indicates an agent failed its task.
	AgentFailed Kind = "agent.failed"
	// PolicyBlocked indicates a policy blocked an action.
	PolicyBlocked Kind = "policy.blocked"
	// VerificationStarted indicates a verification began.
	VerificationStarted Kind = "verification.started"
	// VerificationFailed indicates a verification failed.
	VerificationFailed Kind = "verification.failed"
	// PRUpdated indicates a pull request was updated.
	PRUpdated Kind = "pr.updated"
	// PRMerged indicates a pull request was merged.
	PRMerged Kind = "pr.merged"
	// PRRejected indicates a pull request was rejected.
	PRRejected Kind = "pr.rejected"
	// DeploymentFailed indicates a deployment failed.
	DeploymentFailed Kind = "deployment.failed"
	// RuntimeAnomaly indicates a runtime anomaly was detected.
	RuntimeAnomaly Kind = "runtime.anomaly"
	// CodeChanged indicates code changed.
	CodeChanged Kind = "code.changed"
	// TaskApprovalRequested indicates a task entered a human-approval gate.
	TaskApprovalRequested Kind = "task.approval_requested"
	// TaskApproved indicates a task's approval gate was granted.
	TaskApproved Kind = "task.approved"
)

// Event is a single typed system event.
type Event struct {
	ID      string // stable id; auto-generated when empty on Publish
	Kind    Kind
	Source  string // agent/service that emitted it
	Subject string // subject identifier (task id, incident id, ...)
	Service string // optional affected service

	// Structured tracing fields (F-53, spec §55). These complement the
	// free-form Payload map with typed fields that consumers can filter on.
	ProjectID    string `json:"project_id,omitempty"`
	RepositoryID string `json:"repository_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	Provenance   string `json:"provenance,omitempty"` // e.g. "loop", "mcp", "web", "incident"

	// EventVersion is the schema version of this event ( ).
	// Defaults to 1 on Publish when zero. Consumers can use it to handle
	// evolving event schemas safely.
	EventVersion int `json:"event_version,omitempty"`

	// EntityRefs are optional references to related entities (files, symbols,
	// services, etc.) that consumers can use for correlation without parsing
	// the Payload.
	EntityRefs []string `json:"entity_refs,omitempty"`

	Payload    any       // optional structured payload
	OccurredAt time.Time // defaults to now when zero on Publish
}

// Handler receives an event asynchronously on Publish.
type Handler func(Event)

// Bus is an in-memory publish/subscribe bus with bounded history. A
// subscription with the zero Kind ("") receives ALL events.
type Bus struct {
	mu      sync.Mutex
	subs    []*subscription
	history []Event
	max     int
	eid     uint64 // monotonic event id suffix (Bug: ID collision)
	wg      sync.WaitGroup

	// P4.3 idempotency: events whose ID was already published are de-duplicated
	// (not delivered twice). Bounded by idemCap with FIFO eviction.
	seen      map[string]struct{}
	seenOrder []string
	idemCap   int

	// P4.4 retry/dead-letter: a handler that panics is retried up to
	// retryMax times with retryBackoff between attempts; a handler that
	// exhausts its retries has its event routed to the dead-letter queue.
	retryMax     int
	retryBackoff time.Duration
	deadLetter   []*deadLetterSub

	// P4.5 persisted replay: when persistPath is non-empty, every published
	// event (ID + OccurredAt + Kind + payload) is appended as a JSON line so a
	// later bus can replay the history.
	persistPath string

	sem chan struct{} // bounded worker semaphore for handler dispatch
}

type subscription struct {
	kind   Kind
	active bool
	handle Handler
}

// deadLetterSub is a handler registered for the dead-letter queue (P4.4).
// It receives events whose normal handlers exhausted their retries.
type deadLetterSub struct {
	active bool
	handle Handler
}

// defaultIdempotencyCap bounds the remembered ID set for the default-on
// idempotency (P4.3). Producers that publish a stable, non-empty event ID and
// retry the same event are deduplicated; the cap (FIFO eviction) prevents
// unbounded growth on long-lived buses.
const defaultIdempotencyCap = 10000

// New returns a bus with a bounded history of 100 events and idempotency
// (P4.3) enabled by default so duplicate delivery of the same event ID never
// duplicates side effects. Call EnableIdempotency(cap) afterward to change the
// remembered-ID capacity, or pass cap 0 for an unbounded set.
func New() *Bus {
	return &Bus{
		max:     100,
		seen:    make(map[string]struct{}),
		idemCap: defaultIdempotencyCap,
		sem:     make(chan struct{}, 128),
	}
}

// Subscribe registers a handler for a kind (empty kind = all events) and
// returns an unsubscribe func. Calling unsubscribe is idempotent and safe.
// Unsubscribing removes the subscription from the bus so the captured handler
// closure can be garbage-collected (Bug #6: no retained-leak).
func (b *Bus) Subscribe(kind Kind, h Handler) func() {
	sub := &subscription{kind: kind, active: true, handle: h}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if !sub.active {
			return // idempotent
		}
		sub.active = false
		// Remove from the slice via swap-with-last so the captured closure is
		// released rather than retained forever.
		for i, s := range b.subs {
			if s == sub {
				last := len(b.subs) - 1
				b.subs[i] = b.subs[last]
				b.subs[last] = nil // allow GC of the tail reference
				b.subs = b.subs[:last]
				break
			}
		}
	}
}

// payloadTooLarge reports whether a payload should be withheld from history.
// We serialize it to JSON; any payload that can't be serialized is treated as
// too large rather than risk unbounded retention.
func payloadTooLarge(p any) bool {
	if p == nil {
		return false
	}
	b, err := json.Marshal(p)
	if err != nil {
		return true
	}
	return len(b) > maxHistoryPayloadSize
}

// Publish delivers ev to every matching active subscription asynchronously
// (each handler runs in its own goroutine, so a slow or panicking subscriber
// never blocks the publisher) and appends it to history (capped at max). ID
// and OccurredAt default when zero.
// Idempotency (P4.3): if ev.ID is non-empty and was already published, the
// event is dropped silently — producers may retry publishing the same event
// without causing duplicate delivery.
func (b *Bus) Publish(ev Event) {
	if ev.ID == "" {
		// Combine a monotonic counter with the timestamp so events published
		// within the same nanosecond never collide (Bug: ID collision).
		ev.ID = fmt.Sprintf("e-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&b.eid, 1))
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}
	if ev.EventVersion == 0 {
		ev.EventVersion = 1 // Default schema version
	}

	// P4.3 dedup: a non-empty ID that was already seen is dropped. This runs
	// before building the history entry so retries don't pollute history.
	b.mu.Lock()
	if b.seen != nil {
		if _, dup := b.seen[ev.ID]; dup {
			b.mu.Unlock()
			return
		}
		b.rememberLocked(ev.ID)
	}
	b.mu.Unlock()

	b.mu.Lock()
	var targets []Handler
	for _, sub := range b.subs {
		if sub.active && (sub.kind == "" || sub.kind == ev.Kind) {
			targets = append(targets, sub.handle)
		}
	}
	// Bound history by count and by payload size (Bug #19).
	hist := ev
	if payloadTooLarge(ev.Payload) {
		hist.Payload = nil
	}
	b.history = append(b.history, hist)
	if len(b.history) > b.max {
		// Drop the oldest entries beyond the bound.
		b.history = b.history[len(b.history)-b.max:]
	}
	persistPath := b.persistPath
	b.mu.Unlock()

	// P4.5: persist the event before delivery so a crash after dispatch can be
	// replayed from the last durable point.
	if persistPath != "" {
		_ = b.appendPersist(ev)
	}

	// Deliver after releasing the lock so handlers may safely re-enter the
	// bus. Each handler runs in its own goroutine: this keeps Publish
	// non-blocking (Bug #16) and lets us recover from subscriber panics so a
	// bad handler can't crash the process (Bug #1). Handlers that panic are
	// retried (P4.4) and, on exhaustion, dead-lettered.
	b.wg.Add(len(targets))
	for _, h := range targets {
		go func(handler func(Event), e Event) {
			defer b.wg.Done()
			if b.sem != nil {
				select {
				case b.sem <- struct{}{}:
					defer func() { <-b.sem }()
				default:
					// Proceed without blocking when saturated to avoid deadlocks on re-entrant calls
				}
			}
			b.deliverWithRetry(handler, e)
		}(h, ev)
	}
}

// deliverWithRetry runs h(e), recovering panics. On a panic it retries up to
// retryMax times with retryBackoff between attempts; if retries are exhausted
// (or disabled) the event is routed to the dead-letter queue (P4.4).
func (b *Bus) deliverWithRetry(h Handler, e Event) {
	b.mu.Lock()
	max, backoff := b.retryMax, b.retryBackoff
	b.mu.Unlock()

	for attempt := 0; ; attempt++ {
		if !runSafe(h, e) {
			if attempt < max {
				time.Sleep(backoff)
				continue
			}
			b.enqueueDeadLetter(e)
			return
		}
		return
	}
}

// runSafe invokes h, recovering panics. It reports whether the handler
// completed without panicking.
func runSafe(h Handler, e Event) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			// Log rather than silently swallow the panic.
			log.Printf("eventbus: subscriber panic: %v", r)
			ok = false
		}
	}()
	h(e)
	return true
}

// rememberLocked records an event ID for idempotency, evicting the oldest when
// the cap is reached. Caller must hold b.mu.
func (b *Bus) rememberLocked(id string) {
	if _, ok := b.seen[id]; ok {
		return
	}
	b.seen[id] = struct{}{}
	b.seenOrder = append(b.seenOrder, id)
	if b.idemCap > 0 && len(b.seenOrder) > b.idemCap {
		old := b.seenOrder[0]
		b.seenOrder = b.seenOrder[1:]
		delete(b.seen, old)
	}
}

// enqueueDeadLetter routes an event to the dead-letter queue, delivering it to
// every registered dead-letter handler in its own goroutine. Safe to call
// from any delivery goroutine.
func (b *Bus) enqueueDeadLetter(e Event) {
	b.mu.Lock()
	var targets []Handler
	for _, dl := range b.deadLetter {
		if dl.active {
			targets = append(targets, dl.handle)
		}
	}
	b.mu.Unlock()

	if len(targets) == 0 {
		return
	}
	b.wg.Add(len(targets))
	for _, h := range targets {
		go func(hd Handler, ev Event) {
			defer b.wg.Done()
			_ = runSafe(hd, ev)
		}(h, e)
	}
}

// Flush blocks until every handler goroutine launched by Publish so far has
// returned. Tests use this to assert deterministically on asynchronous
// delivery; production callers (webhooks, audit) do not need to call it.
func (b *Bus) Flush() {
	b.wg.Wait()
}

// History returns a copy of stored events matching kind (empty = all), oldest
// first.
func (b *Bus) History(kind Kind) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Event, 0, len(b.history))
	for _, ev := range b.history {
		if kind == "" || kind == ev.Kind {
			out = append(out, ev)
		}
	}
	return out
}

// Len returns the current number of active subscriptions.
func (b *Bus) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := 0
	for _, sub := range b.subs {
		if sub.active {
			n++
		}
	}
	return n
}

// EnableIdempotency turns on P4.3 de-duplication with the given capacity. Any
// event published with an ID that was already published is dropped. cap bounds
// the remembered ID set (FIFO eviction); pass 0 for an unbounded set (not
// recommended for long-lived buses).
func (b *Bus) EnableIdempotency(cap int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen = make(map[string]struct{})
	b.seenOrder = nil
	b.idemCap = cap
}

// SetRetryPolicy configures P4.4 delivery retries: a handler that panics is
// retried up to maxRetries times with the given backoff, then the event is
// dead-lettered. maxRetries 0 (default) means no retry — a panicking handler
// is dead-lettered immediately.
func (b *Bus) SetRetryPolicy(maxRetries int, backoff time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if maxRetries < 0 {
		maxRetries = 0
	}
	if backoff < 0 {
		backoff = 0
	}
	b.retryMax = maxRetries
	b.retryBackoff = backoff
}

// SubscribeDeadLetter registers a handler that receives every event whose
// normal subscribers exhausted their retries (P4.4). Returns an idempotent
// unsubscribe func.
func (b *Bus) SubscribeDeadLetter(h Handler) func() {
	b.mu.Lock()
	dl := &deadLetterSub{active: true, handle: h}
	b.deadLetter = append(b.deadLetter, dl)
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !dl.active {
			return
		}
		dl.active = false
		for i, s := range b.deadLetter {
			if s == dl {
				b.deadLetter = append(b.deadLetter[:i], b.deadLetter[i+1:]...)
				break
			}
		}
	}
}

// EnablePersistence turns on P4.5 event persistence: every published event is
// appended as one JSON line to path (created if absent, parent dirs created).
// EnableReplay() on the same or a new bus re-delivers those events. Persistence
// is best-effort: an append failure is logged and does not fail Publish.
func (b *Bus) EnablePersistence(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.persistPath = path
}

// DefaultPersistPath returns the conventional per-root event persistence
// file: <root>/.kern/events.jsonl. Callers may persist anywhere, but the
// guard, kern-server, and the relay all converge on this path so the
// persisted file and the live socket describe the same event stream.
func DefaultPersistPath(root string) string {
	return filepath.Join(root, ".kern", "events.jsonl")
}

// appendPersist appends ev as a JSON line to persistPath. Best-effort.
func (b *Bus) appendPersist(ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		log.Printf("eventbus: persist marshal error: %v", err)
		return err
	}
	if b.persistPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.persistPath), 0o755); err != nil {
		log.Printf("eventbus: persist mkdir error: %v", err)
		return err
	}
	f, err := os.OpenFile(b.persistPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("eventbus: persist open error: %v", err)
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("eventbus: persist write error: %v", err)
		return err
	}
	return nil
}

// Replay reads a persistence file written by EnablePersistence and re-delivers
// every stored event to the current subscribers via Publish. If idempotency is
// enabled on this bus, replaying the same file twice is a no-op for the second
// pass. Returns the number of events replayed and the first read error (if any).
func (b *Bus) Replay(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return n, err
		}
		b.Publish(ev)
		n++
	}
	return n, nil
}

// splitLines splits raw file bytes into individual lines without trailing \n.
func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
