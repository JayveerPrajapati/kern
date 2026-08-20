package runtime

import (
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Store is an in-memory, deterministic Source. It collects telemetry events,
// deployments and commits and answers the Source query contract. It is used
// directly for local/offline production intelligence and as the sink that
// vendor adapters feed into.
type Store struct {
	events      []Event
	deployments []domain.Deployment
	commits     []Commit
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{}
}

// Name implements Source.
func (s *Store) Name() string { return "local" }

// Ingest appends a telemetry event and keeps events sorted by timestamp.
func (s *Store) Ingest(ev Event) {
	s.events = append(s.events, ev)
	s.sortEvents()
}

// IngestAll appends many events at once.
func (s *Store) IngestAll(evs []Event) {
	s.events = append(s.events, evs...)
	s.sortEvents()
}

func (s *Store) sortEvents() {
	sort.SliceStable(s.events, func(i, j int) bool {
		return s.events[i].Timestamp.Before(s.events[j].Timestamp)
	})
}

// AddDeployment records a deployment.
func (s *Store) AddDeployment(d domain.Deployment) {
	s.deployments = append(s.deployments, d)
}

// AddCommit records a commit.
func (s *Store) AddCommit(c Commit) {
	s.commits = append(s.commits, c)
}

// Events implements Source: all events for a service, or all events when the
// service is empty, sorted by timestamp.
func (s *Store) Events(service string) []Event {
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
	return append([]Commit(nil), s.commits...)
}

// Since returns events at or after the given time. service empty = all services.
func (s *Store) Since(service string, from time.Time) []Event {
	var out []Event
	for _, e := range s.events {
		if !e.Timestamp.Before(from) && (service == "" || e.Service == service) {
			out = append(out, e)
		}
	}
	return out
}
