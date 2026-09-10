package evidence

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func sampleClaims() []domain.Claim {
	return []domain.Claim{
		{
			Statement:  "service a is healthy",
			Confidence: ConfidenceHigh,
			Scope:      "svc-a",
			Evidence: []domain.Evidence{
				{Type: domain.EvidenceTest, Content: "TestA passed", Digest: Digest("TestA passed")},
				{Type: domain.EvidenceRuntime, Content: "latency p99=120ms", Digest: Digest("latency p99=120ms")},
			},
		},
		{
			Statement:  "no recent deploy",
			Confidence: ConfidenceModerate,
			Scope:      "svc-a",
		},
	}
}

func TestBuildTrustChain(t *testing.T) {
	claims := sampleClaims()
	tc := BuildTrustChain(claims)
	if len(tc.Claims) != 2 {
		t.Fatalf("Claims len = %d, want 2", len(tc.Claims))
	}
	// Claim 0 has 2 evidence entries -> 2 links; claim 1 has none -> 0 links.
	if len(tc.Links) != 2 {
		t.Fatalf("Links len = %d, want 2", len(tc.Links))
	}
	from := Digest(claims[0].Statement)
	wantTos := map[string]bool{
		Digest(claims[0].Evidence[0].Content): true,
		Digest(claims[0].Evidence[1].Content): true,
	}
	for _, l := range tc.Links {
		if l.From != from {
			t.Errorf("link From = %q, want %q", l.From, from)
		}
		if l.Confidence != claims[0].Confidence {
			t.Errorf("link Confidence = %v, want %v", l.Confidence, claims[0].Confidence)
		}
		if !wantTos[l.To] {
			t.Errorf("link To = %q not among claim evidence digests", l.To)
		}
		delete(wantTos, l.To)
	}
	if len(wantTos) != 0 {
		t.Errorf("missing evidence digests in links: %v", wantTos)
	}
	// The evidence-free claim is listed in Claims but contributes no links.
	for _, c := range tc.Claims {
		if c.Statement == "no recent deploy" && len(c.Evidence) != 0 {
			t.Errorf("evidence-free claim mutated: %+v", c)
		}
	}
	if len(tc.Links) != 2 {
		t.Errorf("evidence-free claim produced links")
	}
}

func TestVerifyTrustChain_Valid(t *testing.T) {
	tc := BuildTrustChain(sampleClaims())
	if err := VerifyTrustChain(tc); err != nil {
		t.Fatalf("VerifyTrustChain on valid chain: %v", err)
	}
}

func TestVerifyTrustChain_UnknownFrom(t *testing.T) {
	tc := BuildTrustChain(sampleClaims())
	tc.Links[0].From = Digest("some other statement")
	if err := VerifyTrustChain(tc); err == nil {
		t.Fatal("expected error for link whose From matches no claim")
	}
}

func TestVerifyTrustChain_ToNotOnClaim(t *testing.T) {
	tc := BuildTrustChain(sampleClaims())
	tc.Links[0].To = Digest("unrelated content")
	if err := VerifyTrustChain(tc); err == nil {
		t.Fatal("expected error for link whose To is not evidence of the claim")
	}
}

func TestVerifyTrustChain_ConfidenceOutOfRange(t *testing.T) {
	tc := BuildTrustChain(sampleClaims())
	tc.Links[0].Confidence = 1.5
	if err := VerifyTrustChain(tc); err == nil {
		t.Fatal("expected error for confidence outside [0,1]")
	}
	tc.Links[0].Confidence = -0.1
	if err := VerifyTrustChain(tc); err == nil {
		t.Fatal("expected error for negative confidence")
	}
}

func TestBundleVerify_TrustChain(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-trust", ix)
	if err != nil {
		t.Fatal(err)
	}
	// Generate never attaches a trust chain: nil must still verify.
	if b.TrustChain != nil {
		t.Fatal("Generate should leave TrustChain nil")
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("bundle with nil TrustChain must verify: %v", err)
	}
	// A valid chain, re-sealed, must verify.
	b.TrustChain = BuildTrustChain(sampleClaims())
	b.BundleHash = computeBundleHash(b)
	if err := b.Verify(); err != nil {
		t.Fatalf("bundle with valid TrustChain must verify: %v", err)
	}
	// A tampered chain, re-sealed, must fail Verify.
	b.TrustChain = BuildTrustChain(sampleClaims())
	b.TrustChain.Links[0].From = Digest("not a listed claim")
	b.BundleHash = computeBundleHash(b)
	if err := b.Verify(); err == nil {
		t.Fatal("bundle with invalid TrustChain must fail Verify")
	}
}
