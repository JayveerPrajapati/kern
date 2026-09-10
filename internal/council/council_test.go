package council

import (
	"encoding/json"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/reviewpack"
)

func pack(claims, assumptions []reviewpack.ClaimRef, hash string) *reviewpack.ReviewPack {
	return &reviewpack.ReviewPack{
		SchemaVersion: 1,
		ContentHash:   hash,
		Claims:        claims,
		Assumptions:   assumptions,
	}
}

func TestNormalizeConsensusAndDivergence(t *testing.T) {
	p1 := pack(
		[]reviewpack.ClaimRef{
			{Type: "FACT", Status: "observed", Statement: "Count is called on nil", Evidence: 2},
			{Type: "FACT", Status: "observed", Statement: "the server exits cleanly on SIGTERM", Evidence: 1},
		},
		[]reviewpack.ClaimRef{
			{Type: "INFERENCE", Status: "inferred", Statement: "Helper may overflow", Evidence: 0},
		},
		"aaaa1111",
	)
	p2 := pack(
		[]reviewpack.ClaimRef{
			{Type: "FACT", Status: "observed", Statement: "Count is called on nil", Evidence: 3},
			{Type: "HYPOTHESIS", Status: "reported", Statement: "the server exits cleanly on SIGTERM", Evidence: 0},
		},
		nil,
		"bbbb2222",
	)
	p3 := pack(
		[]reviewpack.ClaimRef{
			{Type: "FACT", Status: "observed", Statement: "Count is called on nil", Evidence: 1},
			{Type: "FACT", Status: "observed", Statement: "unique minority claim", Evidence: 0},
		},
		nil,
		"cccc3333",
	)

	r := Normalize([]*reviewpack.ReviewPack{p1, p2, p3})

	// Consensus: "Count is called on nil" appears in all three packs.
	if len(r.Consensus) != 1 {
		t.Fatalf("consensus = %+v", r.Consensus)
	}
	if r.Consensus[0].Statement != "Count is called on nil" {
		t.Errorf("consensus statement = %q", r.Consensus[0].Statement)
	}
	if r.Consensus[0].Evidence != 6 { // 2+3+1
		t.Errorf("consensus evidence = %d", r.Consensus[0].Evidence)
	}
	if len(r.Consensus[0].Packs) != 3 {
		t.Errorf("consensus packs = %v", r.Consensus[0].Packs)
	}

	// Divergence: "the server exits cleanly on SIGTERM" is observed in p1,
	// reported HYPOTHESIS in p2.
	if len(r.Divergence) != 1 {
		t.Fatalf("divergence = %+v", r.Divergence)
	}
	if len(r.Divergence[0].Kinds) != 2 {
		t.Errorf("divergence kinds = %v", r.Divergence[0].Kinds)
	}

	// Minority: claims held by exactly one pack — "unique minority claim"
	// (pack 3) and "Helper may overflow" (pack 1 only).
	if len(r.Minority) != 2 {
		t.Fatalf("minority = %+v", r.Minority)
	}
	foundMin := map[string]bool{}
	for _, m := range r.Minority {
		foundMin[m.Statement] = true
	}
	if !foundMin["unique minority claim"] || !foundMin["Helper may overflow"] {
		t.Errorf("minority = %+v", r.Minority)
	}

	// Unsupported: claims with zero evidence in every pack ("Helper
	// overflow", "unique minority claim"). The SIGTERM claim has evidence
	// in pack 1, so it is supported.
	found := map[string]bool{}
	for _, u := range r.Unsupported {
		found[u.Statement] = true
	}
	if !found["unique minority claim"] || !found["Helper may overflow"] || found["the server exits cleanly on SIGTERM"] {
		t.Errorf("unsupported = %+v", r.Unsupported)
	}

	// Assumptions: any occurrence classified inferred/reported/stale or
	// HYPOTHESIS/INFERENCE — Helper overflow (inferred in p1) and the
	// SIGTERM claim (reported HYPOTHESIS in p2).
	foundA := map[string]bool{}
	for _, a := range r.Assumptions {
		foundA[a.Statement] = true
	}
	if !foundA["Helper may overflow"] || !foundA["the server exits cleanly on SIGTERM"] {
		t.Errorf("assumptions = %+v", r.Assumptions)
	}

	// Decision drivers: consensus ranked first by evidence.
	if len(r.Drivers) != 1 || r.Drivers[0].Statement != "Count is called on nil" {
		t.Errorf("drivers = %+v", r.Drivers)
	}

	// Next verification: divergence + unsupported claims produce actions.
	if len(r.NextVerification) == 0 {
		t.Error("no next verification actions")
	}
	hasVerify := false
	for _, n := range r.NextVerification {
		if n.Action == "verify" {
			hasVerify = true
		}
	}
	if !hasVerify {
		t.Errorf("next verification = %+v", r.NextVerification)
	}
}

func TestNormalizeSinglePack(t *testing.T) {
	p := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "solo claim", Evidence: 1}},
		nil,
		"dddd4444",
	)
	r := Normalize([]*reviewpack.ReviewPack{p})
	if len(r.Consensus) != 0 {
		t.Errorf("consensus with one pack = %+v", r.Consensus)
	}
	if len(r.Minority) != 1 || r.Minority[0].Statement != "solo claim" {
		t.Errorf("minority with one pack = %+v", r.Minority)
	}
	if len(r.Packs) != 1 || r.Packs[0] != "dddd4444" {
		t.Errorf("packs = %v", r.Packs)
	}
}

func TestNormalizeCaseInsensitiveMatching(t *testing.T) {
	p1 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "Count is nil", Evidence: 1}},
		nil,
		"eeee5555",
	)
	p2 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "  count   IS nil ", Evidence: 1}},
		nil,
		"ffff6666",
	)
	r := Normalize([]*reviewpack.ReviewPack{p1, p2})
	if len(r.Consensus) != 1 {
		t.Errorf("case/whitespace-insensitive match failed: %+v", r.Consensus)
	}
}

func TestNormalizeDeterministic(t *testing.T) {
	p1 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "shared", Evidence: 1}},
		nil,
		"aaaa1111",
	)
	p2 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "shared", Evidence: 2}},
		nil,
		"bbbb2222",
	)
	r1 := Normalize([]*reviewpack.ReviewPack{p1, p2})
	r2 := Normalize([]*reviewpack.ReviewPack{p2, p1}) // order must not matter
	if len(r1.Consensus) != len(r2.Consensus) || r1.Consensus[0].Packs[0] != r2.Consensus[0].Packs[0] {
		t.Errorf("order-sensitive: %+v vs %+v", r1.Consensus, r2.Consensus)
	}
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Error("report JSON differs across pack orders")
	}
}

func TestNormalizeNilPacks(t *testing.T) {
	r := Normalize([]*reviewpack.ReviewPack{nil, nil})
	if len(r.Packs) != 0 {
		t.Errorf("packs = %v for nil inputs", r.Packs)
	}
	if len(r.Consensus) != 0 || len(r.Divergence) != 0 {
		t.Error("nil packs produced findings")
	}
}

func TestRenderReport(t *testing.T) {
	p1 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "shared claim", Evidence: 1}},
		nil,
		"aaaa1111",
	)
	p2 := pack(
		[]reviewpack.ClaimRef{{Type: "FACT", Status: "observed", Statement: "shared claim", Evidence: 2}},
		[]reviewpack.ClaimRef{{Type: "HYPOTHESIS", Status: "reported", Statement: "untested hunch", Evidence: 0}},
		"bbbb2222",
	)
	r := Normalize([]*reviewpack.ReviewPack{p1, p2})
	text := RenderReport(r)
	for _, want := range []string{"== council report ==", "=== consensus", "=== minority", "=== assumptions", "=== decision drivers", "=== next verification", "shared claim"} {
		if !contains(text, want) {
			t.Errorf("render missing %q:\n%s", want, text)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
