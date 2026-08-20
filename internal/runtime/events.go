// Package runtime implements production intelligence: a deterministic,
// vendor-agnostic layer that ingests telemetry (metrics, logs, traces, errors),
// deployments and commits and correlates them to a production alert. Core logic
// depends only on the Source interface; concrete adapters plug in without
// coupling core logic to any vendor.
package runtime

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// EventType discriminates the kind of telemetry event.
type EventType string

const (
	EventMetric EventType = "metric"
	EventLog    EventType = "log"
	EventTrace  EventType = "trace"
	EventError  EventType = "error"
)

// Event is the atomic unit of production intelligence: a single metric sample,
// log line, trace span, or error. It is intentionally plain data so it can be
// produced by any adapter and reasoned about deterministically.
type Event struct {
	ID         string
	Type       EventType
	Service    string
	Severity   string // info, warning, error, critical
	Message    string
	Timestamp  time.Time
	TraceID    string
	SpanID     string
	Attributes map[string]string
}

// IsError reports whether the event represents an error or critical condition.
func (e Event) IsError() bool {
	return e.Type == EventError || e.Severity == "error" || e.Severity == "critical"
}

// Commit is a source-control commit relevant to production (e.g. the code
// behind a deployment). Files are the paths changed in the commit.
type Commit struct {
	SHA         string
	Message     string
	Author      string
	Files       []string
	CommittedAt time.Time
}

// Source is the durable, vendor-agnostic production-intelligence adapter
// contract. Core logic (Store, Correlator, the incident engine) depends only on
// this interface so that vendor-specific sources never leak into core logic.
type Source interface {
	// Name identifies the adapter (e.g. "local", "otel", "prometheus").
	Name() string
	// Events returns telemetry events for a service. Empty service = all.
	Events(service string) []Event
	// Deployments returns deployments for a service. Empty service = all.
	Deployments(service string) []domain.Deployment
	// Commits returns the commit history known to this source.
	Commits() []Commit
}
