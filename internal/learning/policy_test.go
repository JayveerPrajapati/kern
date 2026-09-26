package learning

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// decidedApproval returns a decided approval carrying the full binding context
// (requester, risk, policies, artifact) with an explicit decision time.
func decidedApproval(id, requester string, risk domain.RiskLevel, policies []string, artifact, status string, at time.Time) domain.Approval {
	a := domain.Approval{
		ID:          id,
		Requester:   requester,
		Status:      status,
		RequestedAt: at,
		RiskLevel:   risk,
		PolicyIDs:   policies,
		ArtifactID:  artifact,
	}
	t := at.Add(time.Minute)
	a.DecidedAt = &t
	return a
}

// TestPolicyPatternsAlwaysApproved proves the pre-approval signal: a signature
// approved >= threshold times with zero rejections yields ONE RECOMMENDATION
// pattern carrying the deterministic statement, the signature scope, the
// approved count, and the contributing approval IDs as provenance. PolicyIDs
// are sorted regardless of input order.
func TestPolicyPatternsAlwaysApproved(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	// Policies deliberately unordered: the signature must sort them.
	decisions := []domain.Approval{
		decidedApproval("appr-1", "agent-a", domain.RiskHigh, []string{"p2", "p1"}, "art-1", "approved", base),
		decidedApproval("appr-2", "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", "approved", base.Add(time.Hour)),
		decidedApproval("appr-3", "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", "approved", base.Add(2*time.Hour)),
	}

	patterns := PolicyPatterns(decisions, 3)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1: %+v", len(patterns), patterns)
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", p.ClaimType)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3 (approved count)", p.Count)
	}
	wantKey := "approval:agent-a:HIGH:policies=p1,p2:artifact=artifact"
	if p.Key != wantKey {
		t.Errorf("Key = %q, want %q", p.Key, wantKey)
	}
	wantStmt := "pre-approve agent-a HIGH action (p1,p2) — approved 3 times, never rejected"
	if p.Statement != wantStmt {
		t.Errorf("Statement = %q, want %q", p.Statement, wantStmt)
	}
	if len(p.Sample) != 1 || p.Sample[0] != wantStmt {
		t.Errorf("Sample = %v, want [%q]", p.Sample, wantStmt)
	}
	if len(p.Provenance.Sources) != 3 ||
		p.Provenance.Sources[0] != "approval appr-1" ||
		p.Provenance.Sources[2] != "approval appr-3" {
		t.Errorf("Provenance.Sources = %v, want the 3 sorted approval refs", p.Provenance.Sources)
	}
}

// TestPolicyPatternsRisky proves the risky-action signal: any rejection in a
// signature yields an INFERENCE pattern recommending policy review, with the
// total decided count, even when the same signature was also approved (never
// a pre-approval recommendation once a rejection exists).
func TestPolicyPatternsRisky(t *testing.T) {
	base := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	decisions := []domain.Approval{
		decidedApproval("appr-1", "agent-b", domain.RiskCritical, []string{"p1"}, "", "approved", base),
		decidedApproval("appr-2", "agent-b", domain.RiskCritical, []string{"p1"}, "", "rejected", base.Add(time.Hour)),
		decidedApproval("appr-3", "agent-b", domain.RiskCritical, []string{"p1"}, "", "rejected", base.Add(2*time.Hour)),
		// A second signature with rejections only (zero approvals).
		decidedApproval("appr-4", "agent-c", domain.RiskHigh, []string{"p9"}, "", "rejected", base.Add(3*time.Hour)),
	}

	patterns := PolicyPatterns(decisions, 3)
	if len(patterns) != 2 {
		t.Fatalf("patterns = %d, want 2: %+v", len(patterns), patterns)
	}
	// Deterministic: sorted by signature key.
	first := patterns[0]
	if first.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", first.ClaimType)
	}
	if first.Count != 3 {
		t.Errorf("Count = %d, want 3 (total decided)", first.Count)
	}
	wantStmt := "agent-b CRITICAL action (p1) rejected 2 times out of 3 — policy review recommended"
	if first.Statement != wantStmt {
		t.Errorf("Statement = %q, want %q", first.Statement, wantStmt)
	}
	second := patterns[1]
	if second.ClaimType != domain.ClaimInference || second.Count != 1 {
		t.Errorf("second pattern = %+v, want INFERENCE with total 1", second)
	}
	if !strings.Contains(second.Statement, "agent-c HIGH action (p9) rejected 1 times out of 1") {
		t.Errorf("second Statement = %q", second.Statement)
	}
}

// TestPolicyPatternsBelowThresholdAndPending proves the silence cases: a
// signature approved below the threshold with no rejections produces nothing,
// and pending approvals never contribute to any signal.
func TestPolicyPatternsBelowThresholdAndPending(t *testing.T) {
	base := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	decisions := []domain.Approval{
		// Below threshold (2 < 3), never rejected: no signal yet.
		decidedApproval("appr-1", "agent-a", domain.RiskHigh, []string{"p1"}, "", "approved", base),
		decidedApproval("appr-2", "agent-a", domain.RiskHigh, []string{"p1"}, "", "approved", base.Add(time.Hour)),
		// Pending approvals of the same signature: ignored.
		{ID: "appr-pending", Requester: "agent-a", Status: "pending", RiskLevel: domain.RiskHigh, PolicyIDs: []string{"p1"}, RequestedAt: base.Add(2 * time.Hour)},
	}
	if patterns := PolicyPatterns(decisions, 3); len(patterns) != 0 {
		t.Fatalf("below-threshold + pending = %+v, want nothing", patterns)
	}

	// A below-threshold signature with a rejection is risky, not silent.
	decisions = append(decisions,
		decidedApproval("appr-3", "agent-b", domain.RiskHigh, []string{"p1"}, "", "rejected", base.Add(3*time.Hour)))
	patterns := PolicyPatterns(decisions, 3)
	if len(patterns) != 1 {
		t.Fatalf("risky below-threshold = %d, want 1 (rejection outranks threshold)", len(patterns))
	}
	if patterns[0].ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE for the rejected signature", patterns[0].ClaimType)
	}
}

// TestPolicyPatternsDeterministic proves the extractor is order-independent:
// the same decisions in a different order (and with shuffled policy slices)
// yield byte-identical patterns, and output is sorted by signature key.
func TestPolicyPatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	inputs := [][]domain.Approval{
		{
			decidedApproval("appr-1", "agent-a", domain.RiskHigh, []string{"p2", "p1"}, "art-1", "approved", base),
			decidedApproval("appr-2", "agent-b", domain.RiskHigh, []string{"p1"}, "", "rejected", base.Add(time.Hour)),
			decidedApproval("appr-3", "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", "approved", base.Add(2*time.Hour)),
			decidedApproval("appr-4", "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", "approved", base.Add(3*time.Hour)),
			decidedApproval("appr-5", "agent-b", domain.RiskHigh, []string{"p1"}, "", "rejected", base.Add(4*time.Hour)),
		},
		{
			// Same decisions, reversed order and shuffled policy slices.
			decidedApproval("appr-5", "agent-b", domain.RiskHigh, []string{"p1"}, "", "rejected", base.Add(4*time.Hour)),
			decidedApproval("appr-4", "agent-a", domain.RiskHigh, []string{"p2", "p1"}, "art-1", "approved", base.Add(3*time.Hour)),
			decidedApproval("appr-3", "agent-a", domain.RiskHigh, []string{"p2", "p1"}, "art-1", "approved", base.Add(2*time.Hour)),
			decidedApproval("appr-2", "agent-b", domain.RiskHigh, []string{"p1"}, "", "rejected", base.Add(time.Hour)),
			decidedApproval("appr-1", "agent-a", domain.RiskHigh, []string{"p1", "p2"}, "art-1", "approved", base),
		},
	}

	a := PolicyPatterns(inputs[0], 3)
	b := PolicyPatterns(inputs[1], 3)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("input order changed the output:\n%+v\nvs\n%+v", a, b)
	}
	// Sorted by signature key.
	for i := 1; i < len(a); i++ {
		if a[i-1].Key >= a[i].Key {
			t.Errorf("patterns not sorted by key: %q then %q", a[i-1].Key, a[i].Key)
		}
	}
	// Sorted policies reflected in every statement.
	for _, p := range a {
		if !strings.Contains(p.Statement, "(p1,p2)") && p.Key == "approval:agent-a:HIGH:policies=p1,p2:artifact=artifact" {
			t.Errorf("sorted policies missing from statement %q", p.Statement)
		}
	}
}
