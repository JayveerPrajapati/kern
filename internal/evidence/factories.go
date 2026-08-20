package evidence

import (
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

// evidenceFor builds a single Evidence entry with a stable content digest.
func evidenceFor(t domain.EvidenceType, source, content string) domain.Evidence {
	return domain.Evidence{
		Type:      t,
		Source:    source,
		Content:   content,
		Digest:    Digest(content),
		Timestamp: time.Now(),
	}
}

// evidenceForRel builds an Evidence entry with an explicit Relationship kind
// (e.g. "calls", "depends_on"), empty when there is no meaningful relationship.
func evidenceForRel(t domain.EvidenceType, source, content, relationship string) domain.Evidence {
	ev := evidenceFor(t, source, content)
	ev.Relationship = relationship
	return ev
}

// FromSecurityFinding wraps a v1 sec.Finding as a FACT claim with confidence
// 1.0 and policy evidence, since security scans are deterministic checks.
func FromSecurityFinding(f sec.Finding) domain.Claim {
	return NewBuilder(domain.ClaimFact, f.Message).
		WithSource("sec").
		WithProvenance("sec:" + f.Rule).
		WithScope(f.File).
		WithConfidence(ConfidenceCertain).
		WithEvidence(evidenceFor(domain.EvidencePolicy, "sec", f.File+":"+f.Message+":"+f.Snippet)).
		Build()
}

// FromGraphImpact wraps an impact/blast-radius result as an INFERENCE claim
// with graph evidence and high (0.9) confidence, since the conclusion is
// derived from the call/dependency graph.
func FromGraphImpact(target string, affected []string) domain.Claim {
	content := "target: " + target + "\naffected:\n" + indent(affected)
	statement := fmt.Sprintf("%s has impact on %d symbol(s)", target, len(affected))
	return NewBuilder(domain.ClaimInference, statement).
		WithSource("intel").
		WithProvenance("intel:graph").
		WithScope(target).
		WithConfidence(ConfidenceHigh).
		WithEvidence(evidenceForRel(domain.EvidenceGraph, "intel", content, "depends_on")).
		Build()
}

// FromTestResult wraps a test pass/fail as a FACT claim with confidence 1.0
// and test evidence.
func FromTestResult(pkg string, passed bool, output string) domain.Claim {
	status := "passed"
	if !passed {
		status = "failed"
	}
	content := "go test " + pkg + " " + status + "\n" + output
	statement := fmt.Sprintf("Tests %s for package %s", status, pkg)
	return NewBuilder(domain.ClaimFact, statement).
		WithSource("verify").
		WithProvenance("verify:test").
		WithScope(pkg).
		WithConfidence(ConfidenceCertain).
		WithEvidence(evidenceFor(domain.EvidenceTest, "verify", content)).
		Build()
}

// FromBuildResult wraps a build pass/fail as a FACT claim with confidence 1.0
// and build evidence.
func FromBuildResult(pkg string, passed bool, output string) domain.Claim {
	status := "succeeded"
	if !passed {
		status = "failed"
	}
	content := "go build " + pkg + " " + status + "\n" + output
	statement := fmt.Sprintf("Build %s for package %s", status, pkg)
	return NewBuilder(domain.ClaimFact, statement).
		WithSource("verify").
		WithProvenance("verify:build").
		WithScope(pkg).
		WithConfidence(ConfidenceCertain).
		WithEvidence(evidenceFor(domain.EvidenceBuild, "verify", content)).
		Build()
}

// FromGitChange wraps a git diff as a FACT claim with confidence 1.0 and git
// evidence describing what changed.
func FromGitChange(file, diff string) domain.Claim {
	content := "git diff " + file + "\n" + diff
	statement := "File " + file + " changed"
	return NewBuilder(domain.ClaimFact, statement).
		WithSource("git").
		WithProvenance("git:diff").
		WithScope(file).
		WithConfidence(ConfidenceCertain).
		WithEvidence(evidenceForRel(domain.EvidenceGit, "git", content, "changed_by")).
		Build()
}

// FromMemoryRecall wraps a recalled memory as an INFERENCE claim with
// moderate (0.8) confidence and memory evidence, since recalled memory is
// historical and may not reflect the current state.
func FromMemoryRecall(mem domain.Memory) domain.Claim {
	return NewBuilder(domain.ClaimInference, mem.Content).
		WithSource("memory").
		WithProvenance("memory:recall").
		WithScope(mem.Scope).
		WithConfidence(ConfidenceModerate).
		WithEvidence(evidenceFor(domain.EvidenceMemory, mem.Source, mem.Content)).
		Build()
}

// FromPolicyEvaluation wraps a policy check result as a FACT claim with
// confidence 1.0 and policy evidence.
func FromPolicyEvaluation(rule string, allowed bool, reason string) domain.Claim {
	verdict := "ALLOWED"
	if !allowed {
		verdict = "DENIED"
	}
	content := "policy:" + rule + " -> " + verdict + "\n" + reason
	statement := fmt.Sprintf("Policy %s %s: %s", rule, verdict, reason)
	return NewBuilder(domain.ClaimFact, statement).
		WithSource("policy").
		WithProvenance("policy:" + rule).
		WithScope("all").
		WithConfidence(ConfidenceCertain).
		WithEvidence(evidenceFor(domain.EvidencePolicy, "policy", content)).
		Build()
}

// FromHypothesis wraps an unverified proposition (e.g. a suspected root cause)
// as a HYPOTHESIS claim with moderate (0.8) confidence and graph evidence.
func FromHypothesis(statement, scope, provenance string, evidence domain.Evidence) domain.Claim {
	return NewBuilder(domain.ClaimHypothesis, statement).
		WithSource("evidence").
		WithProvenance(provenance).
		WithScope(scope).
		WithConfidence(ConfidenceModerate).
		WithEvidence(evidence).
		Build()
}

// FromRecommendation wraps a suggested action as a RECOMMENDATION claim,
// carrying confidence reflecting how strongly the analysis supports it.
func FromRecommendation(statement, scope, provenance string, confidence float64, evidence domain.Evidence) domain.Claim {
	return NewBuilder(domain.ClaimRecommendation, statement).
		WithSource("evidence").
		WithProvenance(provenance).
		WithScope(scope).
		WithConfidence(confidence).
		WithEvidence(evidence).
		Build()
}

// indent prefixes each line of s with "- " for readable list content.
func indent(lines []string) string {
	if len(lines) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  - ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
