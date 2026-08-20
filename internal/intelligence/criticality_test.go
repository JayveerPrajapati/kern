package intelligence

import (
	"errors"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// criticalityGraph returns a graph with a center symbol "X" having exactly n
// direct callers, each an isolated leaf that only calls X. This keeps each
// symbol's dependent count exact and independent (no transitive accumulation).
func criticalityGraph(center string, n int) Graph {
	var nodes []domain.Node
	var edges []domain.Edge
	nodes = append(nodes, domain.Node{ID: center, Kind: "symbol", Label: center})
	for i := 0; i < n; i++ {
		id := "caller_" + string(rune('a'+i))
		nodes = append(nodes, domain.Node{ID: id, Kind: "symbol", Label: id})
		edges = append(edges, domain.Edge{From: id, To: center, Kind: "calls"})
	}
	return Graph{Graph: domain.Graph{Nodes: nodes, Edges: edges}}
}

func TestProductionCriticality(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"isolated", 0, "low"},
		{"couple", 2, "low"},
		{"boundary_medium_low", 3, "medium"},
		{"service", 5, "medium"},
		{"boundary_medium_high", 9, "medium"},
		{"boundary_high", 10, "high"},
		{"platform", 12, "high"},
		{"boundary_high_critical", 20, "high"},
		{"boundary_critical", 21, "critical"},
		{"core", 30, "critical"},
	}
	for _, tc := range cases {
		g := criticalityGraph("X", tc.n)
		if got := g.ProductionCriticality("X"); got != tc.want {
			t.Errorf("%s: ProductionCriticality(n=%d) = %q, want %q", tc.name, tc.n, got, tc.want)
		}
	}
}

// mockIncidentStore is a minimal IncidentReader fake for tests.
type mockIncidentStore struct {
	incidents []domain.Incident
	err       error
}

func (m *mockIncidentStore) List() ([]domain.Incident, error) { return m.incidents, m.err }

func TestWhatIncidentsAffected(t *testing.T) {
	g := FromIndex(fakeIndex()) // module node "svc" is the affected service for "Foo".

	// Matches "Foo"'s affected services only when AffectedService == "svc".
	store := &mockIncidentStore{incidents: []domain.Incident{
		{ID: "inc-1", Title: "Payments down", Severity: domain.SeverityCritical, AffectedService: "svc"},
		{ID: "inc-2", Title: "Auth latency", Severity: domain.SeverityWarning, AffectedService: "other"},
		{ID: "inc-3", Title: "DB failover", Severity: domain.SeverityError, AffectedService: "svc"},
	}}

	got := WhatIncidentsAffected(&g, "Foo", store)
	if len(got) != 2 {
		t.Fatalf("WhatIncidentsAffected(Foo) = %d incidents, want 2: %+v", len(got), got)
	}
	// Deterministic order: store order, svc entries in incident-list order.
	if got[0].ID != "inc-1" || got[0].Service != "svc" || got[0].Severity != "critical" {
		t.Errorf("first match = %+v, want inc-1/svc/critical", got[0])
	}
	if got[1].ID != "inc-3" || got[1].Severity != string(domain.SeverityError) {
		t.Errorf("second match = %+v, want inc-3/error", got[1])
	}

	// Nil store returns nil.
	if WhatIncidentsAffected(&g, "Foo", nil) != nil {
		t.Error("WhatIncidentsAffected with nil store should return nil")
	}

	// A symbol with no affected services returns nil.
	if got := WhatIncidentsAffected(&g, "Animal", store); got != nil {
		t.Errorf("WhatIncidentsAffected(Animal) = %+v, want nil", got)
	}

	// Store error is swallowed deterministically (returns nil).
	if got := WhatIncidentsAffected(&g, "Foo", &mockIncidentStore{err: errors.New("boom")}); got != nil {
		t.Errorf("WhatIncidentsAffected on store error = %+v, want nil", got)
	}
}
