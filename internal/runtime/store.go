package runtime

import (
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Growth caps for the in-memory Store: a long-lived process with live
// sources polling every 30s would otherwise accumulate events, deployments
// and commits unboundedly. When a cap is exceeded the OLDEST entries are
// dropped. Events and deployments are sorted on the rare overflow so the
// dropped prefix is truly the oldest; commits keep insertion order (their
// read contract), so the front of the slice is the oldest inserted.
const (
	maxEvents      = 10_000
	maxDeployments = 1_000
	maxCommits     = 5_000
)

// Store is an in-memory, deterministic Source. It collects telemetry events,
// deployments and commits and answers the Source query contract. It is used
// directly for local/offline production intelligence and as the sink that
// vendor adapters feed into.
type Store struct {
	events      []Event
	deployments []domain.Deployment
	commits     []Commit
	dirty       bool // events appended since the last sort; readers sort lazily
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{}
}

// Name implements Source.
func (s *Store) Name() string { return "local" }

// Ingest appends a telemetry event. Sorting is deferred until read (the
// dirty flag) — a full O(n log n) re-sort on every single event was the hot
// path for live sources. On overflow the oldest events are dropped; that
// sort is rare, so it is amortized away.
func (s *Store) Ingest(ev Event) {
	s.events = append(s.events, ev)
	s.dirty = true
	s.trimEvents()
}

// IngestAll appends many events at once.
func (s *Store) IngestAll(evs []Event) {
	s.events = append(s.events, evs...)
	s.dirty = true
	s.trimEvents()
}

// trimEvents enforces the maxEvents cap, dropping the OLDEST entries. Only
// on overflow (rare) is the slice sorted first, so the dropped prefix is
// truly the oldest rather than merely the oldest-inserted.
func (s *Store) trimEvents() {
	if len(s.events) <= maxEvents {
		return
	}
	s.sortEvents()
	s.events = s.events[len(s.events)-maxEvents:]
	s.dirty = false
}

func (s *Store) sortEvents() {
	sort.SliceStable(s.events, func(i, j int) bool {
		return s.events[i].Timestamp.Before(s.events[j].Timestamp)
	})
}

// ensureSorted lazily sorts events when ingest has dirtied them since the
// last read. Readers call this first so their output stays time-ordered.
func (s *Store) ensureSorted() {
	if s.dirty {
		s.sortEvents()
		s.dirty = false
	}
}

// AddDeployment records a deployment. Growth is bounded at maxDeployments:
// on overflow the oldest (by DeployedAt, the read order) deployments are
// dropped.
func (s *Store) AddDeployment(d domain.Deployment) {
	s.deployments = append(s.deployments, d)
	if len(s.deployments) > maxDeployments {
		sort.SliceStable(s.deployments, func(i, j int) bool {
			return s.deployments[i].DeployedAt.Before(s.deployments[j].DeployedAt)
		})
		s.deployments = s.deployments[len(s.deployments)-maxDeployments:]
	}
}

// AddCommit records a commit. Growth is bounded at maxCommits: on overflow
// the oldest-inserted commits are dropped (insertion order is the read
// contract, so the front of the slice is the oldest).
func (s *Store) AddCommit(c Commit) {
	s.commits = append(s.commits, c)
	if len(s.commits) > maxCommits {
		s.commits = s.commits[len(s.commits)-maxCommits:]
	}
}

// Events implements Source: all events for a service, or all events when the
// service is empty, sorted by timestamp.
func (s *Store) Events(service string) []Event {
	s.ensureSorted()
	if service == "" {
		return append([]Event(nil), s.events...)
	}
	var out []Event
	for _, e := range s.events {
		if e.Service == service {
			out = append(out, e)
		}
	}
	return out
}

// Deployments implements Source: deployments for a service, or all when empty.
func (s *Store) Deployments(service string) []domain.Deployment {
	s.ensureSorted()
	var out []domain.Deployment
	for _, d := range s.deployments {
		if service == "" || d.Service == service {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].DeployedAt.Before(out[j].DeployedAt)
	})
	return out
}

// Commits implements Source.
func (s *Store) Commits() []Commit {
	s.ensureSorted()
	return append([]Commit(nil), s.commits...)
}

// Since returns events at or after the given time. service empty = all services.
func (s *Store) Since(service string, from time.Time) []Event {
	s.ensureSorted()
	var out []Event
	for _, e := range s.events {
		if !e.Timestamp.Before(from) && (service == "" || e.Service == service) {
			out = append(out, e)
		}
	}
	return out
}
