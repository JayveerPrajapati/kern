package evidence

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestValidateClaim(t *testing.T) {
	ev := []domain.Evidence{
		{Content: "c1", Digest: "d1"},
		{Content: "c2", Digest: "d2"},
	}
	tests := []struct {
		name    string
		claim   domain.Claim
		wantErr bool
	}{
		{"observed with evidence and digests", domain.Claim{Statement: "s", Status: domain.ClaimStatusObserved, Evidence: ev}, false},
		{"observed without evidence", domain.Claim{Statement: "s", Status: domain.ClaimStatusObserved}, true},
		{"observed with evidence missing digest", domain.Claim{Statement: "s", Status: domain.ClaimStatusObserved, Evidence: []domain.Evidence{{Content: "c1"}}}, true},
		{"verified_derived with evidence", domain.Claim{Statement: "s", Status: domain.ClaimStatusVerifiedDerived, Evidence: ev}, false},
		{"verified_derived without evidence", domain.Claim{Statement: "s", Status: domain.ClaimStatusVerifiedDerived}, true},
		{"inferred confidence 0.5", domain.Claim{Statement: "s", Status: domain.ClaimStatusInferred, Confidence: 0.5}, false},
		{"inferred confidence 1.0", domain.Claim{Statement: "s", Status: domain.ClaimStatusInferred, Confidence: 1.0}, true},
		{"reported no requirements", domain.Claim{Statement: "s", Status: domain.ClaimStatusReported}, false},
		{"stale no requirements", domain.Claim{Statement: "s", Status: domain.ClaimStatusStale}, false},
		{"unclassified always valid", domain.Claim{Statement: "s"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClaim(tt.claim)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateClaim() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDownrankStale(t *testing.T) {
	now := time.Now()
	maxAge := 24 * time.Hour
	old := domain.Claim{Statement: "old", Timestamp: now.Add(-2 * maxAge), Confidence: 1.0, Status: domain.ClaimStatusObserved}
	extremeOld := domain.Claim{Statement: "extreme", Timestamp: now.Add(-30 * maxAge), Confidence: 0.3, Status: domain.ClaimStatusObserved}
	fresh := domain.Claim{Statement: "fresh", Timestamp: now, Confidence: 0.9, Status: domain.ClaimStatusObserved}
	unclassifiedFresh := domain.Claim{Statement: "unclassified-fresh", Timestamp: now, Confidence: 0.7}
	unclassifiedOld := domain.Claim{Statement: "unclassified-old", Timestamp: now.Add(-2 * maxAge), Confidence: 0.8}

	input := []domain.Claim{old, extremeOld, fresh, unclassifiedFresh, unclassifiedOld}
	out := DownrankStale(input, maxAge, now)

	if len(out) != len(input) {
		t.Fatalf("output len = %d, want %d", len(out), len(input))
	}
	// Old claim: stale + confidence halved (1.0 -> 0.5).
	if out[0].Status != domain.ClaimStatusStale {
		t.Errorf("old claim status = %q, want stale", out[0].Status)
	}
	if out[0].Confidence != 0.5 {
		t.Errorf("old claim confidence = %v, want 0.5", out[0].Confidence)
	}
	// Extreme old: halved then floored at 0.2 (0.3 -> 0.15 -> 0.2).
	if out[1].Status != domain.ClaimStatusStale {
		t.Errorf("extreme claim status = %q, want stale", out[1].Status)
	}
	if out[1].Confidence != 0.2 {
		t.Errorf("extreme claim confidence = %v, want 0.2 (floor)", out[1].Confidence)
	}
	// Fresh claim: unchanged.
	if out[2].Status != domain.ClaimStatusObserved || out[2].Confidence != 0.9 {
		t.Errorf("fresh claim changed: %+v", out[2])
	}
	// Unclassified fresh claim stays unclassified.
	if out[3].Status != "" || out[3].Confidence != 0.7 {
		t.Errorf("unclassified fresh claim changed: %+v", out[3])
	}
	// Unclassified old claim becomes stale, confidence halved (0.8 -> 0.4).
	if out[4].Status != domain.ClaimStatusStale || out[4].Confidence != 0.4 {
		t.Errorf("unclassified old claim = %+v, want stale with 0.4", out[4])
	}
	// Input slice and its claims are never mutated.
	if len(input) != 5 {
		t.Errorf("input slice length changed: %d", len(input))
	}
	if input[0].Status != domain.ClaimStatusObserved || input[0].Confidence != 1.0 {
		t.Errorf("input claim 0 mutated: %+v", input[0])
	}
	if input[1].Status != domain.ClaimStatusObserved || input[1].Confidence != 0.3 {
		t.Errorf("input claim 1 mutated: %+v", input[1])
	}
	if input[4].Status != "" || input[4].Confidence != 0.8 {
		t.Errorf("input claim 4 mutated: %+v", input[4])
	}
}

func TestDownrankStale_BoundaryNotStale(t *testing.T) {
	// A claim exactly at the cutoff (Timestamp == now-maxAge) is not "older
	// than maxAge" and must be preserved unchanged.
	now := time.Now()
	maxAge := time.Hour
	edge := domain.Claim{Statement: "edge", Timestamp: now.Add(-maxAge), Confidence: 0.6, Status: domain.ClaimStatusReported}
	out := DownrankStale([]domain.Claim{edge}, maxAge, now)
	if out[0].Status != domain.ClaimStatusReported || out[0].Confidence != 0.6 {
		t.Errorf("cutoff claim changed: %+v", out[0])
	}
}
