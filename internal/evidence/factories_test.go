package evidence

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

func assertClaim(t *testing.T, c domain.Claim, wantType domain.ClaimType, wantSource string, wantConf float64) {
	t.Helper()
	if c.Type != wantType {
		t.Errorf("Type = %q, want %q", c.Type, wantType)
	}
	if c.Source != wantSource {
		t.Errorf("Source = %q, want %q", c.Source, wantSource)
	}
	if c.Confidence != wantConf {
		t.Errorf("Confidence = %v, want %v", c.Confidence, wantConf)
	}
	if c.Statement == "" {
		t.Error("Statement is empty")
	}
	if c.Timestamp.IsZero() {
		t.Error("Timestamp is zero; want it set to now")
	}
	if len(c.Evidence) == 0 {
		t.Fatal("no evidence attached")
	}
}

func assertEvidence(t *testing.T, e domain.Evidence, wantType domain.EvidenceType, wantSource string) {
	t.Helper()
	if e.Type != wantType {
		t.Errorf("Evidence.Type = %q, want %q", e.Type, wantType)
	}
	if e.Source != wantSource {
		t.Errorf("Evidence.Source = %q, want %q", e.Source, wantSource)
	}
	if e.Content == "" {
		t.Error("Evidence.Content is empty")
	}
	if e.Digest == "" {
		t.Error("Evidence.Digest is empty")
	}
	if Digest(e.Content) != e.Digest {
		t.Errorf("Digest(%q) = %q, want %q (content mismatch)", e.Content, Digest(e.Content), e.Digest)
	}
}

func TestFromSecurityFinding(t *testing.T) {
	f := sec.Finding{
		File:     "db/main.go",
		Line:     42,
		Rule:     "hardcoded-secret",
		Severity: "error",
		Message:  "hardcoded secret found",
		Snippet:  "password = \"x\"",
	}
	c := FromSecurityFinding(f)
	assertClaim(t, c, domain.ClaimFact, "sec", ConfidenceCertain)
	if c.Scope != f.File {
		t.Errorf("Scope = %q, want %q", c.Scope, f.File)
	}
	if c.Provenance != "sec:"+f.Rule {
		t.Errorf("Provenance = %q, want sec:%s", c.Provenance, f.Rule)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidencePolicy, "sec")
}

func TestFromGraphImpact(t *testing.T) {
	c := FromGraphImpact("pkg.Foo", []string{"pkg.Bar", "pkg.Baz"})
	assertClaim(t, c, domain.ClaimInference, "intel", ConfidenceHigh)
	if c.Scope != "pkg.Foo" {
		t.Errorf("Scope = %q, want pkg.Foo", c.Scope)
	}
	if c.Provenance != "intel:graph" {
		t.Errorf("Provenance = %q, want intel:graph", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidenceGraph, "intel")
}

func TestFromGraphImpactEmptyAffected(t *testing.T) {
	c := FromGraphImpact("pkg.Leaf", nil)
	assertClaim(t, c, domain.ClaimInference, "intel", ConfidenceHigh)
	if len(c.Evidence) == 0 {
		t.Fatal("expected at least one evidence entry")
	}
	if c.Evidence[0].Content == "" {
		t.Error("evidence content should be non-empty even for an empty affected list")
	}
}

func TestFromTestPass(t *testing.T) {
	c := FromTestResult("github.com/x/y", true, "ok")
	assertClaim(t, c, domain.ClaimFact, "verify", ConfidenceCertain)
	assertEvidence(t, c.Evidence[0], domain.EvidenceTest, "verify")
}

func TestFromTestFail(t *testing.T) {
	c := FromTestResult("github.com/x/y", false, "--- FAIL: TestX")
	assertClaim(t, c, domain.ClaimFact, "verify", ConfidenceCertain)
	assertEvidence(t, c.Evidence[0], domain.EvidenceTest, "verify")
}

func TestFromBuildPass(t *testing.T) {
	c := FromBuildResult("cmd/kern", true, "  ")
	assertClaim(t, c, domain.ClaimFact, "verify", ConfidenceCertain)
	assertEvidence(t, c.Evidence[0], domain.EvidenceBuild, "verify")
}

func TestFromBuildFail(t *testing.T) {
	c := FromBuildResult("cmd/kern", false, "build failed")
	assertClaim(t, c, domain.ClaimFact, "verify", ConfidenceCertain)
	assertEvidence(t, c.Evidence[0], domain.EvidenceBuild, "verify")
}

func TestFromGitChange(t *testing.T) {
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-foo\n+bar\n"
	c := FromGitChange("main.go", diff)
	assertClaim(t, c, domain.ClaimFact, "git", ConfidenceCertain)
	if c.Scope != "main.go" {
		t.Errorf("Scope = %q, want main.go", c.Scope)
	}
	if c.Provenance != "git:diff" {
		t.Errorf("Provenance = %q, want git:diff", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidenceGit, "git")
}

func TestFromMemoryRecall(t *testing.T) {
	mem := domain.Memory{
		Type:      domain.MemoryLesson,
		Content:   "prefer immutable config",
		Source:    "agent-7",
		Scope:     "config/auth",
		CreatedAt: time.Now(),
	}
	c := FromMemoryRecall(mem)
	assertClaim(t, c, domain.ClaimInference, "memory", ConfidenceModerate)
	if c.Scope != "config/auth" {
		t.Errorf("Scope = %q, want config/auth", c.Scope)
	}
	if c.Provenance != "memory:recall" {
		t.Errorf("Provenance = %q, want memory:recall", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidenceMemory, "agent-7")
}

func TestFromPolicyAllowed(t *testing.T) {
	c := FromPolicyEvaluation("no-delete", true, "allowed by default")
	assertClaim(t, c, domain.ClaimFact, "policy", ConfidenceCertain)
	if c.Provenance != "policy:no-delete" {
		t.Errorf("Provenance = %q, want policy:no-delete", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidencePolicy, "policy")
}

func TestFromPolicyDenied(t *testing.T) {
	c := FromPolicyEvaluation("no-delete", false, "deletion not permitted")
	assertClaim(t, c, domain.ClaimFact, "policy", ConfidenceCertain)
	assertEvidence(t, c.Evidence[0], domain.EvidencePolicy, "policy")
}

func TestDigestIsStable(t *testing.T) {
	const content = "some deterministic evidence content"
	first := Digest(content)
	if Digest(content) != first {
		t.Error("Digest must be stable for identical content")
	}
	if Digest(content) == Digest(content+"!") {
		t.Error("Digest should differ when content changes")
	}
	if Digest("") == "" {
		t.Error("Digest of empty content should be a non-empty hash")
	}
}

func TestFromHypothesis(t *testing.T) {
	c := FromHypothesis("suspected root cause is X", "svc/payments", "rootcause:candidate", evidenceFor(domain.EvidenceGraph, "intel", "graph edges"))
	assertClaim(t, c, domain.ClaimHypothesis, "evidence", ConfidenceModerate)
	if c.Scope != "svc/payments" {
		t.Errorf("Scope = %q, want svc/payments", c.Scope)
	}
	if c.Provenance != "rootcause:candidate" {
		t.Errorf("Provenance = %q, want rootcause:candidate", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidenceGraph, "intel")
}

func TestFromRecommendation(t *testing.T) {
	ev := evidenceFor(domain.EvidenceGraph, "whatif", "kind: remove_symbol\naffected:\npkg.Bar")
	c := FromRecommendation("revert the schema migration", "1", "whatif:simulate", ConfidenceModerate, ev)
	assertClaim(t, c, domain.ClaimRecommendation, "evidence", ConfidenceModerate)
	if c.Scope != "1" {
		t.Errorf("Scope = %q, want 1", c.Scope)
	}
	if c.Provenance != "whatif:simulate" {
		t.Errorf("Provenance = %q, want whatif:simulate", c.Provenance)
	}
	assertEvidence(t, c.Evidence[0], domain.EvidenceGraph, "whatif")
}

func TestDigestAllowsIntegrityCheck(t *testing.T) {
	content := "go test pkg ok\n"
	ev := evidenceFor(domain.EvidenceTest, "verify", content)
	if ev.Digest != Digest(content) {
		t.Errorf("Evidence.Digest = %q, want %q", ev.Digest, Digest(content))
	}
}
