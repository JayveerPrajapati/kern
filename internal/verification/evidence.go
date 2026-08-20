package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ToEvidence wraps a verification result as evidence for the governance layer.
// Build results are typed EvidenceBuild; everything else is EvidenceTest.
func ToEvidence(verdict Verdict, result *VerificationResult) domain.Evidence {
	content := fmt.Sprintf("verification=%s: %s", verdict, result.Summary)
	if result.Summary == "" {
		content = fmt.Sprintf("verification=%s", verdict)
	}
	typ := domain.EvidenceTest
	if result.Build != nil {
		typ = domain.EvidenceBuild
	}
	sum := sha256.Sum256([]byte(content))
	return domain.Evidence{
		Type:      typ,
		Source:    "verification",
		Content:   content,
		Digest:    hex.EncodeToString(sum[:]),
		Timestamp: result.GeneratedAt,
	}
}

// Annotate converts a verification result into claims for the context engine:
// one FACT claim per completed verification and a final INFERENCE claim for the
// overall verdict. Ordering is fixed and deterministic.
func Annotate(result *VerificationResult) []domain.Claim {
	var claims []domain.Claim
	now := result.GeneratedAt
	if now.IsZero() {
		now = time.Now()
	}
	appendFact := func(scope, statement string) {
		claims = append(claims, domain.Claim{
			Type:       domain.ClaimFact,
			Statement:  statement,
			Source:     "verification",
			Provenance: "verification",
			Timestamp:  now,
			Scope:      scope,
			Confidence: 1.0,
		})
	}

	if b := result.Build; b != nil {
		appendFact("build", fmt.Sprintf("build verification %s", okWord(b.OK)))
	}
	if t := result.UnitTests; t != nil {
		appendFact("test", fmt.Sprintf("unit tests %s (%d passed, %d failed, %d skipped)",
			okWord(t.OK), t.Passed, t.Failed, t.Skipped))
	}
	if t := result.Integration; t != nil {
		appendFact("test", fmt.Sprintf("integration tests %s", okWord(t.OK)))
	}
	if s := result.Security; s != nil {
		appendFact("security", fmt.Sprintf("security scan %s (%d findings, %d critical, %d high, %d low)",
			okWord(s.OK), s.Count, s.Critical, s.High, s.Low))
	}
	if a := result.Architecture; a != nil {
		appendFact("architecture", fmt.Sprintf("architecture verification %s (%d violations)",
			okWord(a.OK), len(a.Violations)))
	}
	if d := result.Dependency; d != nil {
		appendFact("dependency", fmt.Sprintf("dependency graph %s (%d nodes, %d edges)",
			okWord(d.OK), d.GraphNodes, d.GraphEdges))
	}
	if e := result.E2ETests; e != nil {
		appendFact("e2e", fmt.Sprintf("e2e tests %s (%d passed, %d failed, %d skipped)",
			okWord(e.OK), e.Passed, e.Failed, e.Skipped))
	}
	if s := result.StaticAnalysis; s != nil {
		appendFact("static-analysis", fmt.Sprintf("static analysis %s (%s, %d findings)",
			okWord(s.OK), s.Tool, len(s.Findings)))
	}
	if p := result.Performance; p != nil {
		appendFact("performance", fmt.Sprintf("benchmarks %s (%d benchmarks)",
			okWord(p.OK), len(p.Benchmarks)))
	}

	claims = append(claims, domain.Claim{
		Type:       domain.ClaimInference,
		Statement:  fmt.Sprintf("overall verification verdict: %s", result.Verdict),
		Source:     "verification",
		Provenance: "aggregated verdict",
		Timestamp:  now,
		Scope:      "all",
		Confidence: 1.0,
	})
	return claims
}

// okWord renders a stable pass/fail label.
func okWord(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// claims returns the evidence-backed claims carried by a sub-result. Nil-safe
// so aggregation never panics on a missing verification.
func (b *BuildResult) claims() []domain.Claim {
	if b == nil {
		return nil
	}
	return b.Claims
}

func (t *TestResult) claims() []domain.Claim {
	if t == nil {
		return nil
	}
	return t.Claims
}

func (s *SecurityResult) claims() []domain.Claim {
	if s == nil {
		return nil
	}
	return s.Claims
}

// evidenceOf assembles the deterministic Evidence list carried by a result.
func evidenceOf(result *VerificationResult) []domain.Evidence {
	return []domain.Evidence{ToEvidence(result.Verdict, result)}
}

// verdictOf derives the overall verdict from the individual results.
func verdictOf(result *VerificationResult) Verdict {
	fail := false
	warn := false
	if b := result.Build; b != nil && !b.OK {
		fail = true
	}
	if t := result.UnitTests; t != nil && !t.OK {
		fail = true
	}
	if t := result.Integration; t != nil && !t.OK {
		fail = true
	}
	if s := result.Security; s != nil {
		if !s.OK {
			fail = true
		}
		if s.Count > 0 {
			warn = true
		}
	}
	if a := result.Architecture; a != nil && !a.OK {
		fail = true
	}
	if d := result.Dependency; d != nil && !d.OK {
		fail = true
	}
	// E2E and static-analysis are hard failures when they report a problem.
	if e := result.E2ETests; e != nil && !e.OK {
		fail = true
	}
	if s := result.StaticAnalysis; s != nil && !s.OK {
		fail = true
	}
	// Performance is advisory ("where available"): a benchmark run returning
	// non-zero does not fail the verdict.
	if fail {
		return VerdictFail
	}
	if warn {
		return VerdictWarn
	}
	return VerdictPass
}
