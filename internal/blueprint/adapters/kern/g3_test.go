package kern

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// requireKern skips if the kern binary isn't available.
func requireKern(t *testing.T) *KernClient {
	t.Helper()
	client, err := NewKernClient()
	if err != nil {
		t.Skipf("kern binary not available: %v", err)
	}
	return client
}

// secretReq builds a ChangeRequest where ALL files in the fixture are "changed",
// so the SecretCheck's changed-file filter doesn't drop them. We pass the
// fixture dir as RepositoryRoot and list every file we wrote as a FileChange.
func secretReq(t *testing.T, fr SecretFixtureResult, files ...string) domain.ChangeRequest {
	t.Helper()
	fc := make([]domain.FileChange, 0, len(files))
	for _, f := range files {
		fc = append(fc, domain.FileChange{Path: f, Op: domain.OpWrite})
	}
	return domain.ChangeRequest{
		RepositoryRoot: fr.Dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          fc,
	}
}

// --- G3 spec scenarios (lines 805-816) ---

// G3-1: obvious API key
func TestG3_ObviousAPIKey(t *testing.T) {
	client := requireKern(t)
	fr := SecretsAPIKey(t)
	req := secretReq(t, fr, "aws.go")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK; findings: %+v", res.Status, res.Findings)
	}
	if len(res.Findings) == 0 {
		t.Fatal("expected >=1 finding for AWS key")
	}
	// Category must mention AWS.
	foundAWS := false
	for _, f := range res.Findings {
		if strings.Contains(f.Explanation, "AWS") || strings.Contains(f.Message, "AWS") {
			foundAWS = true
		}
	}
	if !foundAWS {
		t.Fatalf("no finding identifies AWS category: %+v", res.Findings)
	}
}

// G3-2: password assignment
func TestG3_PasswordAssignment(t *testing.T) {
	client := requireKern(t)
	fr := SecretsPassword(t)
	req := secretReq(t, fr, "auth.go")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	foundPassword := false
	for _, f := range res.Findings {
		if strings.Contains(strings.ToLower(f.Explanation), "password") || strings.Contains(f.Message, "PASSWORD") {
			foundPassword = true
		}
	}
	if !foundPassword {
		t.Fatalf("no finding identifies password category: %+v", res.Findings)
	}
}

// G3-3: private key
func TestG3_PrivateKey(t *testing.T) {
	client := requireKern(t)
	fr := SecretsPrivateKey(t)
	req := secretReq(t, fr, "key.go")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	foundKey := false
	for _, f := range res.Findings {
		if strings.Contains(strings.ToLower(f.Explanation), "private key") {
			foundKey = true
		}
	}
	if !foundKey {
		t.Fatalf("no finding identifies private key category: %+v", res.Findings)
	}
}

// G3-4: token in JSON
func TestG3_TokenInJSON(t *testing.T) {
	client := requireKern(t)
	fr := SecretsTokenJSON(t)
	req := secretReq(t, fr, "config.json")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	foundGithub := false
	for _, f := range res.Findings {
		if strings.Contains(f.Explanation, "GitHub") || strings.Contains(f.Message, "GITHUB") {
			foundGithub = true
		}
	}
	if !foundGithub {
		t.Fatalf("no finding identifies GitHub token category: %+v", res.Findings)
	}
}

// G3-5: token in YAML
func TestG3_TokenInYAML(t *testing.T) {
	client := requireKern(t)
	fr := SecretsTokenYAML(t)
	req := secretReq(t, fr, "secrets.yaml")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	foundPassword := false
	for _, f := range res.Findings {
		if strings.Contains(strings.ToLower(f.Explanation), "password") {
			foundPassword = true
		}
	}
	if !foundPassword {
		t.Fatalf("no finding identifies password category: %+v", res.Findings)
	}
}

// G3-6: false positive test fixture (allowlist suppression)
func TestG3_FalsePositiveTestFixture(t *testing.T) {
	client := requireKern(t)
	fr := SecretsFalsePositive(t)
	// The test fixture file is testdata/fixture.go — should be allowlisted
	// by DefaultAllowlist (testdata/ directory convention).
	req := secretReq(t, fr, "testdata/fixture.go")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Allowlist suppression: test fixture files must not be flagged.
	if res.Status == domain.StatusBlock {
		t.Fatalf("status = BLOCK, want PASS (test fixture should be allowlisted); findings: %+v", res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings for allowlisted test fixture, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// G3-6b: false positive WITHOUT allowlist (verify the secret IS detected when
// allowlist is disabled — proves the suppression is the allowlist's doing).
func TestG3_FalsePositiveNoAllowlist(t *testing.T) {
	client := requireKern(t)
	fr := SecretsFalsePositive(t)
	req := secretReq(t, fr, "testdata/fixture.go")
	// Custom allowlist that allows nothing.
	noneAllowlist := noneAllow{}
	res, err := NewSecretCheckWithAllowlist(client, noneAllowlist).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("without allowlist: status = %s, want BLOCK (secret should be detected)", res.Status)
	}
}

type noneAllow struct{}

func (noneAllow) IsAllowed(string) bool { return false }

// G3-7: redaction in plain text — the secret value must NOT appear in the
// Finding's Message, Explanation, SuggestedFix, or Evidence.
func TestG3_RedactionPlainText(t *testing.T) {
	client := requireKern(t)
	fr := SecretsAPIKey(t)
	req := secretReq(t, fr, "aws.go")
	res, err := NewSecretCheck(client).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	const secretValue = "AKIAIOSFODNN7EXAMPLE"
	for _, f := range res.Findings {
		checkNoLeak(t, "Message", f.Message, secretValue)
		checkNoLeak(t, "Explanation", f.Explanation, secretValue)
		checkNoLeak(t, "SuggestedFix", f.SuggestedFix, secretValue)
		for _, e := range f.Evidence {
			checkNoLeak(t, "Evidence.Description", e.Description, secretValue)
			checkNoLeak(t, "Evidence.Location", e.Location, secretValue)
		}
	}
	if !res.Findings[0].Redacted {
		t.Error("Finding.Redacted = false, want true")
	}
}

// G3-8: redaction in JSON — the secret value must NOT appear in the JSON
// serialization of the ValidationResult.
func TestG3_RedactionJSON(t *testing.T) {
	client := requireKern(t)
	fr := SecretsAPIKey(t)
	req := secretReq(t, fr, "aws.go")
	res, _ := NewSecretCheck(client).Run(context.Background(), req)

	// Wrap in a ValidationResult and marshal to JSON.
	vr := domain.ValidationResult{
		Status:   domain.StatusBlock,
		Findings: res.Findings,
	}
	b, err := json.Marshal(vr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const secretValue = "AKIAIOSFODNN7EXAMPLE"
	if strings.Contains(string(b), secretValue) {
		t.Fatalf("JSON output LEAKS the secret value:\n%s", b)
	}
	// Redacted flag must be present.
	if !strings.Contains(string(b), `"redacted":true`) {
		t.Errorf("JSON missing redacted:true flag:\n%s", b)
	}
}

// G3-9: redaction in logs/audit entries — the secret must not appear in the
// CheckResult's serialized form (simulating a log/audit entry).
func TestG3_RedactionLogs(t *testing.T) {
	client := requireKern(t)
	fr := SecretsPrivateKey(t)
	req := secretReq(t, fr, "key.pem")
	res, _ := NewSecretCheck(client).Run(context.Background(), req)

	// Simulate a log/audit entry by marshaling the CheckResult.
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The PEM marker is not secret, but the key body between markers is.
	// Check that the key body doesn't leak.
	const keyBody = "MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz"
	if strings.Contains(string(b), keyBody) {
		t.Fatalf("log/audit JSON LEAKS the private key body:\n%s", b)
	}
	// Also check the PEM begin marker isn't in the snippet (kern includes it
	// in snippet, but Blueprint must not propagate it).
	if strings.Contains(string(b), "-----BEGIN") {
		t.Errorf("log/audit JSON contains PEM begin marker (snippet leaked):\n%s", b)
	}
}

// --- Helper ---

func checkNoLeak(t *testing.T, field, value, secret string) {
	t.Helper()
	if strings.Contains(value, secret) {
		t.Errorf("%s LEAKS secret value %q: %q", field, secret, value)
	}
}
