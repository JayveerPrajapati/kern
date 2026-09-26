package learnclaim

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestRenderContentMinimal(t *testing.T) {
	got := RenderContent(PatternView{Key: "rate-limiter-flap", Count: 3, Scopes: []string{"api", "gateway"}})
	want := "pattern: rate-limiter-flap recurring 3 times across 2 scope(s)"
	if got != want {
		t.Fatalf("RenderContent() = %q, want %q", got, want)
	}
}

func TestRenderContentZeroValue(t *testing.T) {
	got := RenderContent(PatternView{})
	want := "pattern:  recurring 0 times across 0 scope(s)"
	if got != want {
		t.Fatalf("RenderContent(zero) = %q, want %q", got, want)
	}
}

func TestRenderContentClaimType(t *testing.T) {
	got := RenderContent(PatternView{Key: "k", Count: 1, Scopes: []string{"s"}, ClaimType: domain.ClaimFact})
	if !strings.Contains(got, "claim type: FACT") {
		t.Fatalf("RenderContent() missing claim type, got: %q", got)
	}
}

func TestRenderContentIncidents(t *testing.T) {
	got := RenderContent(PatternView{
		Key: "k", Count: 2, Scopes: []string{"s"},
		Incidents: []string{"inc-1", "inc-2"},
	})
	want := "failure class recurs: incidents inc-1, inc-2"
	if !strings.Contains(got, want) {
		t.Fatalf("RenderContent() missing incidents, got: %q", got)
	}
}

func TestRenderContentStatementVerbatim(t *testing.T) {
	got := RenderContent(PatternView{
		Key: "k", Count: 1, Scopes: []string{"s"},
		Statement: "calibration: verified",
	})
	if !strings.Contains(got, "\ncalibration: verified") {
		t.Fatalf("RenderContent() missing statement, got: %q", got)
	}
}

func TestRenderContentRecommendationFallback(t *testing.T) {
	got := RenderContent(PatternView{
		Key: "k", Count: 1, Scopes: []string{"s"},
		Recommendation: "keep the backoff",
	})
	if !strings.Contains(got, "\nkeep the backoff") {
		t.Fatalf("RenderContent() missing recommendation, got: %q", got)
	}
}

// TestRenderContentStatementPrecedence pins the documented precedence:
// an explicit Statement wins over Recommendation.
func TestRenderContentStatementPrecedence(t *testing.T) {
	got := RenderContent(PatternView{
		Key: "k", Count: 1, Scopes: []string{"s"},
		Statement:      "verdict: ok",
		Recommendation: "do not render me",
	})
	if !strings.Contains(got, "verdict: ok") {
		t.Fatalf("RenderContent() missing statement, got: %q", got)
	}
	if strings.Contains(got, "do not render me") {
		t.Fatalf("RenderContent() rendered recommendation despite statement, got: %q", got)
	}
}

func TestRenderProvenanceNoSources(t *testing.T) {
	if got := RenderProvenance(PatternView{}); got != "" {
		t.Fatalf("RenderProvenance(no sources) = %q, want empty", got)
	}
}

func TestRenderProvenanceSortsSources(t *testing.T) {
	p := PatternView{Provenance: ClaimProvenance{
		Sources: []string{"z-memory", "a-memory", "m-memory"},
		Count:   3,
	}}
	got := RenderProvenance(p)
	want := "sources: a-memory, m-memory, z-memory; count: 3"
	if got != want {
		t.Fatalf("RenderProvenance() = %q, want %q", got, want)
	}
}

// TestRenderProvenanceDoesNotMutateInput pins the defensive copy: the
// caller's slice order must be untouched by the internal sort.
func TestRenderProvenanceDoesNotMutateInput(t *testing.T) {
	srcs := []string{"z-memory", "a-memory"}
	p := PatternView{Provenance: ClaimProvenance{Sources: srcs, Count: 2}}
	RenderProvenance(p)
	if srcs[0] != "z-memory" || srcs[1] != "a-memory" {
		t.Fatalf("RenderProvenance mutated caller slice: %v", srcs)
	}
}

func TestRenderProvenanceLatest(t *testing.T) {
	latest := time.Date(2026, 9, 24, 10, 30, 0, 0, time.UTC)
	p := PatternView{Provenance: ClaimProvenance{
		Sources: []string{"s1"},
		Count:   1,
		Latest:  latest,
	}}
	got := RenderProvenance(p)
	want := "sources: s1; count: 1; latest: 2026-09-24T10:30:00Z"
	if got != want {
		t.Fatalf("RenderProvenance() = %q, want %q", got, want)
	}
}

// TestRenderProvenanceLatestLocalTZ pins UTC normalization: a timestamp in
// a non-UTC zone must render in RFC3339 UTC.
func TestRenderProvenanceLatestLocalTZ(t *testing.T) {
	latest := time.Date(2026, 9, 24, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	p := PatternView{Provenance: ClaimProvenance{
		Sources: []string{"s1"},
		Count:   1,
		Latest:  latest,
	}}
	got := RenderProvenance(p)
	if !strings.Contains(got, "2026-09-24T10:00:00Z") {
		t.Fatalf("RenderProvenance() latest not UTC-normalized, got: %q", got)
	}
}
