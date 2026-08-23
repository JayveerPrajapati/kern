package app

import (
	"testing"
)

// TestCapabilityDiscoverRanked verifies Phase 6.9 discovery ranks results by
// relevance deterministically.
func TestCapabilityDiscoverRanked(t *testing.T) {
	reg := NewCapabilityRegistry()

	// "security" must surface the security capability first with score > 0.
	sec := reg.Discover("security")
	if len(sec) == 0 {
		t.Fatal("Discover(\"security\") returned no results")
	}
	if sec[0].Capability.Name != "security" {
		t.Fatalf("expected security first, got %q", sec[0].Capability.Name)
	}
	if sec[0].Score <= 0 {
		t.Fatalf("expected score > 0 for security, got %v", sec[0].Score)
	}

	// "deploy to production" must include deploy among the results.
	deploy := reg.Discover("deploy to production")
	found := false
	for _, m := range deploy {
		if m.Capability.Name == "deploy" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Discover(\"deploy to production\") did not include the deploy capability")
	}

	// A garbage query must return nothing (no false positives).
	empty := reg.Discover("garbage query zzzqqq")
	if len(empty) != 0 {
		t.Fatalf("expected no matches for garbage query, got %d", len(empty))
	}
}

// TestCapabilitySearchHelper verifies the Search convenience wrapper returns
// the matched capabilities in a deterministic order.
func TestCapabilitySearchHelper(t *testing.T) {
	reg := NewCapabilityRegistry()

	res := reg.Search("verify")
	found := false
	for _, c := range res {
		if c.Name == "verify" {
			found = true
		}
	}
	if !found {
		t.Fatal("Search(\"verify\") did not include the verify capability")
	}

	// Deterministic ordering: results must be ordered descending by score,
	// ties broken by name. We verify the ordering matches Discover.
	disc := reg.Discover("verify")
	if len(disc) != len(res) {
		t.Fatalf("Search/Discover length mismatch: %d vs %d", len(res), len(disc))
	}
	for i := range disc {
		if disc[i].Capability.Name != res[i].Name {
			t.Fatalf("ordering mismatch at %d: Discover=%q Search=%q",
				i, disc[i].Capability.Name, res[i].Name)
		}
	}
}

// TestCapabilityDiscoverSurfaces verifies the Matches field is populated with
// the query terms that overlapped a capability's text.
func TestDiscoverMatchesSurface(t *testing.T) {
	reg := NewCapabilityRegistry()

	res := reg.Discover("understand how the code works")
	if len(res) == 0 {
		t.Fatal("expected results for a relevant query")
	}
	matched := false
	for _, m := range res {
		if m.Capability.Name == "understand" {
			matched = true
			if len(m.Matches) == 0 {
				t.Fatal("expected non-empty Matches for the understand capability")
			}
			break
		}
	}
	if !matched {
		t.Fatal("understand capability not present in discovery results")
	}
}