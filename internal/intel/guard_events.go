package intel

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// Guard event builders shared by `kern guard check` (CLI) and the
// kern_guard_check MCP tool so both surfaces emit identical events for the
// same violations.
//
// IDs are assigned here rather than left to Bus.Publish: a relay owner that
// re-persists a live-emitted copy writes a duplicate line carrying the SAME
// id, which eventbus idempotency (P4.3) collapses at replay.

var guardEventSeq uint64

func guardEventID() string {
	return fmt.Sprintf("guard-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&guardEventSeq, 1))
}

// GuardEvent converts a boundary or purity violation into the
// ArchitectureViolation event published by the guard.
func GuardEvent(v Violation) eventbus.Event {
	subject := v.Symbol
	if subject == "" {
		subject = v.CallerFile
	}
	return eventbus.Event{
		ID:      guardEventID(),
		Kind:    eventbus.ArchitectureViolation,
		Source:  "guard",
		Subject: subject,
		Payload: fmt.Sprintf("%s -> %s forbidden by rule %s -> %s", v.CallerFile, v.Symbol, v.RuleFrom, v.RuleTo),
		AgentID: "guard",
	}
}

// GuardNotConfiguredEvent is the ArchitectureWarning the guard publishes when
// no boundary rules are configured — an unenforced architecture stays visible
// instead of silently passing.
func GuardNotConfiguredEvent() eventbus.Event {
	return eventbus.Event{
		ID:      guardEventID(),
		Kind:    eventbus.ArchitectureWarning,
		Source:  "guard",
		Payload: "boundaries not configured — architecture guard NOT enforced",
		AgentID: "guard",
	}
}

// GuardEvents converts guard outcomes — violations plus the optional
// not-configured warning — into the event batch published by both the CLI
// guard check and the kern_guard_check MCP tool. Order is deterministic:
// violations in order, then the warning.
func GuardEvents(violations []Violation, warnNotConfigured bool) []eventbus.Event {
	events := make([]eventbus.Event, 0, len(violations)+1)
	for _, v := range violations {
		events = append(events, GuardEvent(v))
	}
	if warnNotConfigured {
		events = append(events, GuardNotConfiguredEvent())
	}
	return events
}
