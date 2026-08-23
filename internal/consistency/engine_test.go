package consistency

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// fakeSource is a deterministic Source for tests.
type fakeSource struct {
	name      domain.KnowledgeSource
	version   string
	updatedAt time.Time
	claims    map[string]string
}

func (f fakeSource) Name() domain.KnowledgeSource    { return f.name }
func (f fakeSource) Version() string                 { return f.version }
func (f fakeSource) UpdatedAt() time.Time            { return f.updatedAt }
func (f fakeSource) Claim(sub string) (string, bool) { v, ok := f.claims[sub]; return v, ok }

func TestNoConflict(t *testing.T) {
	now := time.Now()
	sources := []Source{
		fakeSource{name: domain.SourceGraph, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
		fakeSource{name: domain.SourceTwin, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
		fakeSource{name: domain.SourceGit, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
	}
	res := NewEngine().Check(context.Background(), sources, []string{"svc:auth"}, now, 0)
	if res.Overall != domain.ConflictNone {
		t.Fatalf("overall = %q, want NO_CONFLICT", res.Overall)
	}
	if len(res.Report.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Report.Conflicts)
	}
}

func TestConflict(t *testing.T) {
	now := time.Now()
	sources := []Source{
		fakeSource{name: domain.SourceGraph, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
		fakeSource{name: domain.SourceTwin, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "rest"}},
		fakeSource{name: domain.SourceGit, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "rest"}},
	}
	res := NewEngine().Check(context.Background(), sources, []string{"svc:auth"}, now, 0)
	if res.Overall != domain.ConflictPresent {
		t.Fatalf("overall = %q, want CONFLICT", res.Overall)
	}
	if len(res.Report.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(res.Report.Conflicts))
	}
	c := res.Report.Conflicts[0]
	if c.Subject != "svc:auth" {
		t.Errorf("subject = %q", c.Subject)
	}
	if c.ClaimA == c.ClaimB {
		t.Errorf("claims should differ: %q vs %q", c.ClaimA, c.ClaimB)
	}
}

func TestStaleInvalidatesOnVersionMismatch(t *testing.T) {
	now := time.Now()
	// Twin has an older version (v1) than the others (v2) → STALE.
	sources := []Source{
		fakeSource{name: domain.SourceGraph, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
		fakeSource{name: domain.SourceTwin, version: "v1", updatedAt: now.Add(-time.Hour), claims: map[string]string{"svc:auth": "rest"}},
		fakeSource{name: domain.SourceGit, version: "v2", updatedAt: now, claims: map[string]string{"svc:auth": "grpc"}},
	}
	res := NewEngine().Check(context.Background(), sources, []string{"svc:auth"}, now, 0)

	// The stale twin is downgraded, its claim is excluded, so the fresh pair
	// agrees → no CONFLICT, but the overall is STALE.
	if res.Overall != domain.ConflictStale {
		t.Fatalf("overall = %q, want STALE", res.Overall)
	}
	if len(res.Report.Conflicts) != 0 {
		t.Fatalf("stale sources must be excluded from conflicts, got %+v", res.Report.Conflicts)
	}
	found := false
	for _, s := range res.Report.StaleSubjects {
		if s == string(domain.SourceTwin) {
			found = true
		}
	}
	if !found {
		t.Errorf("Twin not marked stale: %v", res.Report.StaleSubjects)
	}
	if len(res.Invalidations) == 0 {
		t.Error("expected invalidation markers for stale sources")
	}
	if res.Report.ConfidenceDowngrades[string(domain.SourceTwin)] != 0.5 {
		t.Errorf("expected downgrade for twin, got %v", res.Report.ConfidenceDowngrades)
	}
}

func TestStaleByFreshnessBound(t *testing.T) {
	now := time.Now()
	// Bound of 1h makes the one-day-old graph stale even though its version
	// matches the (also stale) twin; both stale → STALE.
	sources := []Source{
		fakeSource{name: domain.SourceGraph, version: "v2", updatedAt: now.Add(-2 * time.Hour), claims: map[string]string{"svc:auth": "grpc"}},
		fakeSource{name: domain.SourceTwin, version: "v2", updatedAt: now.Add(-2 * time.Hour), claims: map[string]string{"svc:auth": "grpc"}},
	}
	res := NewEngine().Check(context.Background(), sources, []string{"svc:auth"}, now, time.Hour)
	if res.Overall != domain.ConflictStale {
		t.Fatalf("overall = %q, want STALE via freshness bound", res.Overall)
	}
}

func TestUnknownNoClaims(t *testing.T) {
	now := time.Now()
	sources := []Source{
		fakeSource{name: domain.SourceGraph, version: "v2", updatedAt: now, claims: map[string]string{}},
	}
	res := NewEngine().Check(context.Background(), sources, []string{"svc:auth"}, now, 0)
	if res.Overall != domain.ConflictUnknown {
		t.Fatalf("overall = %q, want UNKNOWN", res.Overall)
	}
}

func TestConflictExplanation(t *testing.T) {
	c := domain.ConsistencyConflict{
		Subject:     "svc:auth",
		ClaimA:      "grpc",
		SourceA:     domain.SourceGraph,
		VersionA:    "v2",
		ClaimB:      "rest",
		SourceB:     domain.SourceTwin,
		VersionB:    "v2",
		SourceNewer: domain.SourceGraph,
	}
	exp := c.Explain()
	if !strings.Contains(exp, "svc:auth") {
		t.Errorf("explanation missing subject: %q", exp)
	}
	if !strings.Contains(exp, "GRAPH") || !strings.Contains(exp, "TWIN") {
		t.Errorf("explanation must name both sources: %q", exp)
	}
	if !strings.Contains(exp, "grpc") || !strings.Contains(exp, "rest") {
		t.Errorf("explanation must include both claims: %q", exp)
	}
	if !strings.Contains(exp, "newer source") {
		t.Errorf("explanation must note the newer source: %q", exp)
	}
	if !strings.Contains(exp, "re-validate GRAPH") {
		t.Errorf("explanation must give a next recommended check: %q", exp)
	}
}

func TestOverallPriority(t *testing.T) {
	// STALE beats UNKNOWN, CONFLICT beats STALE.
	if got := highest(domain.ConflictNone, domain.ConflictUnknown); got != domain.ConflictUnknown {
		t.Errorf("none vs unknown = %q", got)
	}
	if got := highest(domain.ConflictUnknown, domain.ConflictStale); got != domain.ConflictStale {
		t.Errorf("unknown vs stale = %q", got)
	}
	if got := highest(domain.ConflictStale, domain.ConflictPresent); got != domain.ConflictPresent {
		t.Errorf("stale vs conflict = %q", got)
	}
}
