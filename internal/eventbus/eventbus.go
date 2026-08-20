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
	"sync"
	"sync/atomic"
	"time"
)

// maxHistoryPayloadSize bounds the serialized size of a payload retained in
// history (Bug #19). Larger payloads are dropped from history to keep the
// bounded-capacity history bounded in bytes as well as count.
const maxHistoryPayloadSize = 4 << 10 // 4 KiB

// Kind discriminates the class of system event crossing the bus (spec §52).
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
}

type subscription struct {
	kind   Kind
	active bool
	handle Handler
}

// New returns an empty bus with a bounded history of 100 events.
func New() *Bus { return &Bus{max: 100} }

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
func (b *Bus) Publish(ev Event) {
	if ev.ID == "" {
		// Combine a monotonic counter with the timestamp so events published
		// within the same nanosecond never collide (Bug: ID collision).
		ev.ID = fmt.Sprintf("e-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&b.eid, 1))
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}

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
	b.mu.Unlock()

	// Deliver after releasing the lock so handlers may safely re-enter the
	// bus. Each handler runs in its own goroutine: this keeps Publish
	// non-blocking (Bug #16) and lets us recover from subscriber panics so a
	// bad handler can't crash the process (Bug #1).
	b.wg.Add(len(targets))
	for _, h := range targets {
		go func(handler func(Event), e Event) {
			defer b.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					// Log rather than silently swallow the panic.
					log.Printf("eventbus: subscriber panic: %v", r)
				}
			}()
			handler(e)
		}(h, ev)
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
