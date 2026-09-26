package learning

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// seedMemory populates the store with the given memories and returns it.
func seedMemory(t *testing.T, ms []domain.Memory) *memory.MemoryStore {
	t.Helper()
	store := memory.NewMemoryStore(t.TempDir())
	for _, m := range ms {
		if _, err := store.Add(m); err != nil {
			t.Fatalf("Add(%q) failed: %v", m.Content, err)
		}
	}
	return store
}

// incident returns a deterministic incident memory with an explicit timestamp.
func incident(content, scope string, at time.Time) domain.Memory {
	return domain.Memory{
		Type:      domain.MemoryIncident,
		Content:   content,
		Source:    "sre",
		Scope:     scope,
		CreatedAt: at,
	}
}

func TestPatternExtractionGroupsByScope(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		incident("checkout latency spike", "service:checkout", base),
		incident("checkout timeout", "service:checkout", base.Add(1*time.Hour)),
		incident("checkout crash loop", "service:checkout", base.Add(2*time.Hour)),
		incident("payments rejected", "service:payments", base.Add(3*time.Hour)),
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}

	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d: %+v", len(patterns), patterns)
	}

	// Count desc means service:checkout (3) sorts before service:payments (1).
	top := patterns[0]
	if top.Key != "scope:service:checkout" {
		t.Fatalf("expected first pattern key scope:service:checkout, got %q", top.Key)
	}
	if top.Count != 3 {
		t.Fatalf("expected checkout count 3, got %d", top.Count)
	}
	if top.Sample[0] != "checkout crash loop" {
		t.Fatalf("expected newest checkout sample first, got %q", top.Sample[0])
	}
	if len(top.Sample) != 3 {
		t.Fatalf("expected checkout sample capped at 3, got %d", len(top.Sample))
	}
	if len(top.Scopes) != 1 || top.Scopes[0] != "service:checkout" {
		t.Fatalf("expected distinct scope [service:checkout], got %v", top.Scopes)
	}
}

func TestPatternExtractionGroupsBySignature(t *testing.T) {
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// No Scope -> grouped by stable content signature; two identical contents
	// (after trimming) share one signature.
	store := seedMemory(t, []domain.Memory{
		{Type: domain.MemoryIncident, Content: "  duplicate failure  ", CreatedAt: base},
		{Type: domain.MemoryIncident, Content: "duplicate failure", CreatedAt: base.Add(time.Hour)},
		{Type: domain.MemoryLesson, Content: "unique lesson", CreatedAt: base.Add(2 * time.Hour)},
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("expected 2 signature patterns, got %d: %+v", len(patterns), patterns)
	}
	// The duplicate-content group has Count 2 and must sort first.
	if patterns[0].Count != 2 {
		t.Fatalf("expected duplicate group count 2, got %d", patterns[0].Count)
	}
	if patterns[0].Key != "sig:"+contentHash12("duplicate failure") {
		t.Fatalf("unexpected signature key %q", patterns[0].Key)
	}
}

func TestPatternIncidentsAndRecommendation(t *testing.T) {
	p := Pattern{
		Key:            "scope:svc:checkout",
		Count:          3,
		Sample:         []string{"N+1 query in checkout loop"},
		Incidents:      []string{"INC-492", "INC-381"},
		Recommendation: "Use batch/eager loading",
	}
	if len(p.Incidents) != 2 || p.Incidents[0] != "INC-492" {
		t.Errorf("Incidents = %v, want [INC-492 INC-381]", p.Incidents)
	}
	if p.Recommendation != "Use batch/eager loading" {
		t.Errorf("Recommendation = %q, want \"Use batch/eager loading\"", p.Recommendation)
	}
}

// incidentMemory returns an incident-derived memory shaped like the one
// internal/incident writes (buildIncidentLesson): Type MemoryIncident, Scope
// = affected service (the error signature), Subject = incident ID.
func incidentMemory(id, service, content string, at time.Time) domain.Memory {
	return domain.Memory{
		Type:       domain.MemoryIncident,
		Content:    content,
		Source:     "incident-engine",
		Scope:      service,
		Tags:       []string{"incident", "resolved", "postmortem"},
		Subject:    id,
		Provenance: "incident:" + id,
		CreatedAt:  at,
	}
}

// TestIncidentPatternsGroupAndSurface proves recurring incidents group into a
// single pattern (Incidents populated with the incident IDs), Surface(2)
// surfaces ONLY the recurring failure class, and Remember() writes a
// failure-recurs constraint; singleton incidents never surface.
func TestIncidentPatternsGroupAndSurface(t *testing.T) {
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		incidentMemory("INC-101", "checkout", "incident INC-101 in checkout: checkout latency spike; root cause: cache miss", base),
		incidentMemory("INC-102", "checkout", "incident INC-102 in checkout: checkout timeout; root cause: cache miss", base.Add(time.Hour)),
		incidentMemory("INC-103", "payments", "incident INC-103 in payments: payments rejected", base.Add(2*time.Hour)),
		{Type: domain.MemoryLesson, Content: "unique lesson", Source: "loop", Scope: "project", CreatedAt: base.Add(3 * time.Hour)},
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}
	// Three groups: scope:checkout (2 recurring incidents), scope:payments
	// (1 singleton), scope:project (1 non-incident singleton).
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d: %+v", len(patterns), patterns)
	}
	// Count desc: the recurring class sorts first.
	recurring := patterns[0]
	if recurring.Key != "scope:checkout" || recurring.Count != 2 {
		t.Fatalf("expected recurring scope:checkout count 2 first, got %+v", recurring)
	}
	// Incidents populated with the recurring incident IDs (sorted).
	if len(recurring.Incidents) != 2 || recurring.Incidents[0] != "INC-101" || recurring.Incidents[1] != "INC-102" {
		t.Fatalf("expected Incidents [INC-101 INC-102], got %v", recurring.Incidents)
	}
	// Singleton incident groups carry their own ID; the non-incident group none.
	for _, p := range patterns {
		if p.Key == "scope:payments" {
			if len(p.Incidents) != 1 || p.Incidents[0] != "INC-103" {
				t.Fatalf("expected singleton Incidents [INC-103], got %v", p.Incidents)
			}
		}
		if p.Key == "scope:project" && len(p.Incidents) != 0 {
			t.Fatalf("expected no incidents for the non-incident group, got %v", p.Incidents)
		}
	}

	// Surface(2): only the recurring class crosses the threshold.
	surfaced, err := ex.Surface(2)
	if err != nil {
		t.Fatalf("Surface(2) failed: %v", err)
	}
	if len(surfaced) != 1 || surfaced[0].Key != "scope:checkout" {
		t.Fatalf("expected only scope:checkout surfaced, got %+v", surfaced)
	}

	// Remember() writes the failure-recurs constraint naming class + count +
	// incident IDs.
	got, err := ex.Remember(surfaced[0])
	if err != nil {
		t.Fatalf("Remember() failed: %v", err)
	}
	if got.Type != domain.MemoryConstraint {
		t.Fatalf("expected MemoryConstraint, got %q", got.Type)
	}
	if !strings.Contains(got.Content, "scope:checkout") || !strings.Contains(got.Content, "recurring 2 times") || !strings.Contains(got.Content, "INC-101") || !strings.Contains(got.Content, "INC-102") {
		t.Fatalf("constraint must name the failure class, recurrence count and incident IDs, got %q", got.Content)
	}
}

func TestSurfaceThreshold(t *testing.T) {
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		incident("a1", "service:a", base),
		incident("a2", "service:a", base.Add(time.Hour)),
		incident("b1", "service:b", base.Add(2*time.Hour)),
	})

	ex := New(store)
	surfaced, err := ex.Surface(2)
	if err != nil {
		t.Fatalf("Surface(2) failed: %v", err)
	}
	if len(surfaced) != 1 {
		t.Fatalf("expected 1 surfaced pattern, got %d: %+v", len(surfaced), surfaced)
	}
	if surfaced[0].Key != "scope:service:a" || surfaced[0].Count != 2 {
		t.Fatalf("unexpected surfaced pattern: %+v", surfaced[0])
	}

	// threshold <= 0 is treated as 1 -> both patterns returned.
	all, err := ex.Surface(0)
	if err != nil {
		t.Fatalf("Surface(0) failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 patterns for threshold 0, got %d", len(all))
	}
}

// typedClaimMemory returns a memory carrying claim metadata (a typed-claim
// memory) with deterministic source + timestamp.
func typedClaimMemory(ct domain.ClaimType, content, scope, source string, at time.Time) domain.Memory {
	return domain.Memory{
		Type:      domain.MemoryLesson,
		Content:   content,
		Source:    source,
		Scope:     scope,
		CreatedAt: at,
		ClaimType: ct,
	}
}

// TestTypedClaimPatternsGroupByClaimType proves typed-claim memories group
// separately from plain-text memories about the same scope (claim-prefixed
// keys), that the Pattern carries the claim type + provenance summary
// (sorted sources, count, latest timestamp), that Surface filters by the
// same grouping, and that Remember writes the claim metadata onto the
// constraint.
func TestTypedClaimPatternsGroupByClaimType(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		typedClaimMemory(domain.ClaimFact, "checkout uses cache miss", "svc:checkout", "impact-engine", base),
		typedClaimMemory(domain.ClaimFact, "checkout retries on 503", "svc:checkout", "verify-engine", base.Add(time.Hour)),
		typedClaimMemory(domain.ClaimFact, "checkout idempotent POST", "svc:checkout", "impact-engine", base.Add(2*time.Hour)),
		// Plain-text memories about the SAME scope must not merge with the
		// FACT group.
		{Type: domain.MemoryLesson, Content: "checkout lesson", Source: "sre", Scope: "svc:checkout", CreatedAt: base.Add(3 * time.Hour)},
		{Type: domain.MemoryLesson, Content: "checkout note", Source: "loop", Scope: "svc:checkout", CreatedAt: base.Add(4 * time.Hour)},
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns (claim group + plain group), got %d: %+v", len(patterns), patterns)
	}
	var claimP, plainP *Pattern
	for i := range patterns {
		switch patterns[i].Key {
		case "claim:FACT:scope:svc:checkout":
			claimP = &patterns[i]
		case "scope:svc:checkout":
			plainP = &patterns[i]
		}
	}
	if claimP == nil || plainP == nil {
		t.Fatalf("missing claim:FACT or plain scope pattern: %+v", patterns)
	}

	// Claim group: count 3, ClaimType FACT, provenance preserved (sorted
	// sources, count, latest timestamp).
	if claimP.Count != 3 || claimP.ClaimType != domain.ClaimFact {
		t.Fatalf("claim pattern = %+v, want count 3 type FACT", claimP)
	}
	if len(claimP.Provenance.Sources) != 2 || claimP.Provenance.Sources[0] != "impact-engine" || claimP.Provenance.Sources[1] != "verify-engine" {
		t.Errorf("provenance sources = %v, want sorted [impact-engine verify-engine]", claimP.Provenance.Sources)
	}
	if claimP.Provenance.Count != 3 {
		t.Errorf("provenance count = %d, want 3", claimP.Provenance.Count)
	}
	if !claimP.Provenance.Latest.Equal(base.Add(2 * time.Hour)) {
		t.Errorf("provenance latest = %v, want %v", claimP.Provenance.Latest, base.Add(2*time.Hour))
	}

	// Plain group: count 2, no claim TYPE (plain-text memories are not
	// typed-claim groups); provenance still summarizes their sources.
	if plainP.Count != 2 || plainP.ClaimType != "" {
		t.Errorf("plain pattern = %+v, want count 2 with no claim type", plainP)
	}

	// Surface(3) surfaces only the claim group; Surface(1) both.
	surfaced, err := ex.Surface(3)
	if err != nil {
		t.Fatalf("Surface(3) failed: %v", err)
	}
	if len(surfaced) != 1 || surfaced[0].Key != "claim:FACT:scope:svc:checkout" {
		t.Fatalf("Surface(3) = %+v, want only the FACT group", surfaced)
	}
	all, err := ex.Surface(1)
	if err != nil {
		t.Fatalf("Surface(1) failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("Surface(1) = %d patterns, want 2", len(all))
	}

	// Remember writes the claim type + provenance onto the constraint, and
	// the content names the claim type.
	got, err := ex.Remember(*claimP)
	if err != nil {
		t.Fatalf("Remember() failed: %v", err)
	}
	if got.ClaimType != domain.ClaimFact {
		t.Errorf("constraint ClaimType = %q, want FACT", got.ClaimType)
	}
	if !strings.Contains(got.Provenance, "impact-engine") || !strings.Contains(got.Provenance, "verify-engine") {
		t.Errorf("constraint Provenance = %q, want both sources", got.Provenance)
	}
	if !strings.Contains(got.Content, "claim type: FACT") {
		t.Errorf("constraint content must name the claim type, got %q", got.Content)
	}
}

func TestRemember(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	ex := New(store)
	at := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	p := Pattern{
		Key:     "scope:service:checkout",
		Count:   3,
		Scopes:  []string{"service:checkout"},
		Sample:  []string{"x"},
		Created: at,
	}
	got, err := ex.Remember(p)
	if err != nil {
		t.Fatalf("Remember() failed: %v", err)
	}
	if got.ID == "" {
		t.Fatal("expected Remember() to return a memory with an ID")
	}
	if got.Type != domain.MemoryConstraint {
		t.Fatalf("expected type MemoryConstraint, got %q", got.Type)
	}
	if got.Source != "learning" {
		t.Fatalf("expected source learning, got %q", got.Source)
	}

	// Verify it was actually persisted and retrievable via the store.
	list, err := store.List("")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored memory, got %d", len(list))
	}
	got = list[0]
	if got.Type != domain.MemoryConstraint || got.Source != "learning" {
		t.Fatalf("stored memory mismatch: %+v", got)
	}
	expectedContent := "pattern: scope:service:checkout recurring 3 times across 1 scope(s)"
	if got.Content != expectedContent {
		t.Fatalf("unexpected content:\n got %q\nwant %q", got.Content, expectedContent)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "pattern" || got.Tags[1] != "auto-surfaced" {
		t.Fatalf("unexpected tags: %v", got.Tags)
	}
}
