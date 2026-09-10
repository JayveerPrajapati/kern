package integration

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/eval"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/skills"
)

// TestPhase6to10Integration covers the newer phases end-to-end with real
// packages: governance egress + approvals + audit, secret stripping, the eval
// harness (VerifyTokenReduction at unit level), portable skills
// (load/validate/sign/verify), and output profiles (json/action-first/
// identity).
func TestPhase6to10Integration(t *testing.T) {
	t.Run("egress", testEgress)
	t.Run("secrets", testSecrets)
	t.Run("eval", testEvalTokenReduction)
	t.Run("skills", testSkillsRoundTrip)
	t.Run("profiles", testOutputProfiles)
}

func testEgress(t *testing.T) {
	// Pure policy: local-only denies external hosts, allows localhost.
	localOnly := governance.EgressRule{Policy: governance.EgressLocalOnly}
	if d := governance.CheckEgress(localOnly, "8.8.8.8", 443); d.Allowed {
		t.Error("local-only egress allowed 8.8.8.8:443, want denied")
	}
	if d := governance.CheckEgress(localOnly, "localhost", 443); !d.Allowed {
		t.Error("local-only egress denied localhost:443, want allowed")
	}

	// Firewall: a registered agent under an external-approved rule hits the
	// approval gate, ApproveAction grants it, and the audit log records the
	// egress policy decisions.
	fw := governance.NewFirewall().
		WithAgents(governance.NewAgent("itest", "integration test", "test",
			[]governance.Permission{{Resource: "egress", Action: "connect"}})).
		WithEgressRule(governance.EgressRule{Policy: governance.EgressExternalApproved})

	allowed, dec, appr, err := fw.CheckEgress("itest", "8.8.8.8", 443)
	if err != nil {
		t.Fatalf("CheckEgress: %v", err)
	}
	if allowed {
		t.Error("external egress allowed before approval")
	}
	if !dec.RequiresApproval {
		t.Error("external egress decision did not require approval")
	}
	if appr == nil {
		t.Fatal("external egress returned no pending approval")
	}

	if err := fw.ApproveAction(appr.ID, "human"); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	allowed, dec, _, err = fw.CheckEgress("itest", "8.8.8.8", 443)
	if err != nil {
		t.Fatalf("CheckEgress after approval: %v", err)
	}
	if !allowed || !dec.Allowed {
		t.Errorf("external egress after approval: allowed=%v dec=%+v", allowed, dec)
	}

	// Audit log carries the egress policy decisions (pending + allowed).
	var egressEntries []governance.AuditEntry
	for _, e := range fw.AuditLog().All() {
		if e.Policy == "egress" {
			egressEntries = append(egressEntries, e)
		}
	}
	if len(egressEntries) < 2 {
		t.Fatalf("audit log has %d egress-policy entries, want >= 2 (pending + allowed)", len(egressEntries))
	}
	var sawPending, sawAllowed bool
	for _, e := range egressEntries {
		switch e.Result {
		case "pending":
			sawPending = true
		case "allowed":
			sawAllowed = true
		}
	}
	if !sawPending || !sawAllowed {
		t.Errorf("audit egress entries missing pending/allowed decisions: %+v", egressEntries)
	}
}

func testSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"KERN_EMBED_MODEL=llama3.2",
		"API_TOKEN=sk-live-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"OPENAI_API_KEY=sk-openai",
	}
	out := governance.StripSecrets(env, governance.DefaultSecretFilter())
	joined := strings.Join(out, "\n")
	for _, kept := range []string{"PATH=", "KERN_EMBED_MODEL="} {
		if !strings.Contains(joined, kept) {
			t.Errorf("StripSecrets dropped operational var %q: %v", kept, out)
		}
	}
	for _, stripped := range []string{"API_TOKEN=", "AWS_SECRET_ACCESS_KEY=", "OPENAI_API_KEY="} {
		if strings.Contains(joined, stripped) {
			t.Errorf("StripSecrets kept secret var %q: %v", stripped, out)
		}
	}
}

func testEvalTokenReduction(t *testing.T) {
	// A verbose baseline with the critical evidence in its head (budget.Fit
	// always keeps the first lines), so the harness proves token reduction
	// without losing critical evidence — the phase-11 VerifyTokenReduction
	// property at unit level.
	baseline := "FIXME: user_count is never decremented (retention anchor)\n" +
		strings.Repeat("routine noise line that just adds tokens to the baseline\n", 300)
	sample := eval.Sample{
		Name:             "verify-token-reduction",
		Baseline:         baseline,
		Candidate:        baseline, // harness fits it to Budget via budget.Fit
		CriticalEvidence: []string{"user_count is never decremented"},
	}
	h := eval.NewEvalHarness([]eval.Sample{sample}, 60, []eval.Assertion{
		eval.AssertTokenReduction(0.5),
		eval.AssertEvidenceRetention(1.0),
	})
	res := h.Run()
	if res.TokenReduction <= 0 {
		t.Errorf("TokenReduction = %g, want > 0", res.TokenReduction)
	}
	if res.EvidenceRetention != 1.0 {
		t.Errorf("EvidenceRetention = %g, want 1.0", res.EvidenceRetention)
	}
	if len(res.Samples) != 1 || !res.Samples[0].Passed {
		t.Errorf("eval sample did not pass: %+v", res.Samples)
	}
	for _, a := range res.Rubric {
		if !a.Pass {
			t.Errorf("rubric assertion %s (expected %g, actual %g) failed", a.Type, a.Expected, a.Actual)
		}
	}
}

func testSkillsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "counter-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
version: 1.0.0
author: integration-tests
permissions:
  - source:read
dependencies:
  - base
---
# Counter skill

Fix the counter panic by checking inputs before tick.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	loaded, err := skills.LoadSkillsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadSkillsFromDir: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "counter-skill" {
		t.Fatalf("loaded skills = %+v, want [counter-skill]", loaded)
	}
	s := loaded[0]
	if err := skills.ValidateSkill(s); err != nil {
		t.Errorf("ValidateSkill: %v", err)
	}
	if len(s.Manifest.Permissions) != 1 ||
		s.Manifest.Permissions[0].Resource != "source" ||
		s.Manifest.Permissions[0].Action != "read" {
		t.Errorf("manifest permissions = %+v, want source:read", s.Manifest.Permissions)
	}

	// Sign/verify round-trip with a deterministic seed key.
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	sig, err := skills.SignSkill(s, priv)
	if err != nil {
		t.Fatalf("SignSkill: %v", err)
	}
	if err := skills.VerifySkill(s, sig); err != nil {
		t.Errorf("VerifySkill on authentic skill: %v", err)
	}

	// Tampering with the body fails verification (the body is part of the
	// signed canonical payload).
	tampered := s
	tampered.Body = s.Body + "\n\n# tampered\n"
	if err := skills.VerifySkill(tampered, sig); err == nil {
		t.Error("VerifySkill accepted a tampered body")
	}
}

func testOutputProfiles(t *testing.T) {
	// machine-json parses as JSON with the content intact.
	text := "fix the panic in Count"
	out := profiles.ApplyProfile(profiles.MachineJSONProfile(), text)
	var envelope struct {
		Profile string `json:"profile"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("machine-json output is not valid JSON: %v", err)
	}
	if envelope.Profile != "machine-json" || envelope.Content != text {
		t.Errorf("machine-json envelope = %+v, want profile machine-json with original content", envelope)
	}

	// action-first reorders a 3-line action/non-action mix.
	mixed := "observe: current state is unstable\nfix the panic in Count\nsummarize: done\n"
	reordered := profiles.ApplyProfile(profiles.ActionFirstProfile(), mixed)
	lines := strings.Split(strings.TrimSpace(reordered), "\n")
	if len(lines) != 4 || lines[0] != "fix the panic in Count" || lines[1] != "---" {
		t.Errorf("action-first reorder = %q, want fix line first then separator", reordered)
	}

	// human-readable is the identity profile.
	if got := profiles.ApplyProfile(profiles.HumanReadableProfile(), text); got != text {
		t.Errorf("human-readable profile is not identity: %q", got)
	}
}
