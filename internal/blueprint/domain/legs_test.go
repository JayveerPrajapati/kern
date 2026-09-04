package domain

import "testing"

// TestP23_CheckLegKindClassifiesKnownLegs pins the single source of truth for
// the blocking/advisory split: architecture (incl. projected-import
// violations) and secrets are blocking; duplication and resilience are
// advisory.
func TestP23_CheckLegKindClassifiesKnownLegs(t *testing.T) {
	blocking := []string{"architecture:guard", "secret:scan", "secret:gitleaks"}
	for _, name := range blocking {
		if got := CheckLegKind(name); got != LegKindBlocking {
			t.Errorf("CheckLegKind(%q) = %q, want %q", name, got, LegKindBlocking)
		}
	}
	advisory := []string{
		"duplication:jscpd",
		"duplication:advisory",
		"resilience:scenarios",
		"resilience", // checks_skipped display name for resilience:scenarios
	}
	for _, name := range advisory {
		if got := CheckLegKind(name); got != LegKindAdvisory {
			t.Errorf("CheckLegKind(%q) = %q, want %q", name, got, LegKindAdvisory)
		}
	}
}

// TestP23_CheckLegKindUnknownDefaultsToBlocking: an unclassified check must
// default to blocking — claiming a leg is advisory when it might block would
// understate the risk.
func TestP23_CheckLegKindUnknownDefaultsToBlocking(t *testing.T) {
	if got := CheckLegKind("future:unknown-check"); got != LegKindBlocking {
		t.Errorf("CheckLegKind(unknown) = %q, want %q", got, LegKindBlocking)
	}
}
