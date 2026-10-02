package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/secscan"
)

// TestSecurityHooksWiring verifies Server.securityHooks wires the
// security.Hooks the security-family tool bodies depend on: the session
// index accessors, the security service facade, and the stamped server
// version. White-box (same package): the hooks fields are compared against
// the package's own serverVersion variable.
func TestSecurityHooksWiring(t *testing.T) {
	t.Parallel()
	h := (&Server{}).securityHooks()
	if h.LoadIndex == nil {
		t.Error("securityHooks.LoadIndex must be non-nil")
	}
	if h.ChangedContext == nil {
		t.Error("securityHooks.ChangedContext must be non-nil")
	}
	if h.SecuritySvc == nil {
		t.Error("securityHooks.SecuritySvc must be non-nil")
	}
	if h.ServerVersion != serverVersion {
		t.Errorf("securityHooks.ServerVersion = %q, want %q", h.ServerVersion, serverVersion)
	}
}

// TestSecuritySvcMask verifies the adapter facade redacts a high-entropy
// fake token via pii.DefaultPatterns. The sk-test-… dash form matches the
// STRIPE_DASH pattern. sk-abc123 and AKIAIOSFODNN7EXAMPLE are
// scanner-allowlisted example literals and must NOT be used here — a real
// credential never equals a published example. Note the input must not
// carry a token=/secret= keyword prefix: the TOKEN pattern
// (\b(?:token|secret)...[=:]...) wins the greedy longest-match over the
// value's own STRIPE_DASH label.
func TestSecuritySvcMask(t *testing.T) {
	t.Parallel()
	token := "sk-test-abcdefghijklmnopqrstuvwxyz012345"
	text := "credentials " + token + " for staging"
	res, err := (securitySvc{}).Mask(context.Background(), text, nil)
	if err != nil {
		t.Fatalf("Mask error: %v", err)
	}
	if res.Replaced < 1 {
		t.Errorf("Mask replaced %d secrets, want >= 1 (ByLabel: %v)", res.Replaced, res.ByLabel)
	}
	if strings.Contains(res.Text, token) {
		t.Errorf("masked text still contains the token: %q", res.Text)
	}
	if res.ByLabel["STRIPE_DASH"] < 1 {
		t.Errorf("expected STRIPE_DASH label in ByLabel, got %v", res.ByLabel)
	}
}

// TestSecuritySvcFilterBySeverityAndRender verifies severity allow-list
// filtering and the stable Render format with a max cap.
func TestSecuritySvcFilterBySeverityAndRender(t *testing.T) {
	t.Parallel()
	findings := []secscan.Finding{
		{Rule: "hardcoded-secret", Severity: "error", File: "a.go", Line: 1, Message: "hardcoded secret: AWS"},
		{Rule: "weak-crypto", Severity: "warning", File: "b.go", Line: 2, Message: "deprecated or weak cryptographic primitive"},
		{Rule: "insecure-random", Severity: "info", File: "c.go", Line: 3, Message: "non-cryptographic randomness for security-relevant data"},
	}
	svc := securitySvc{}
	filtered := svc.FilterBySeverity(findings, []string{"error", "info"})
	if len(filtered) != 2 {
		t.Fatalf("FilterBySeverity kept %d findings, want 2", len(filtered))
	}
	for _, f := range filtered {
		if f.Severity != "error" && f.Severity != "info" {
			t.Errorf("FilterBySeverity kept %q severity, want only error/info", f.Severity)
		}
	}
	out := svc.Render(findings, 2)
	if out == "" {
		t.Fatal("Render returned empty output")
	}
	if !strings.Contains(out, "weak-crypto") {
		t.Errorf("Render output missing expected finding: %q", out)
	}
	if !strings.Contains(out, "... and 1 more findings") {
		t.Errorf("Render did not note the truncated finding: %q", out)
	}
}

// TestSecuritySvcScan verifies the facade scans a tree and reports a
// hardcoded-secret finding for a real-looking key, mirroring the noise-test
// conventions in internal/sec: AKIAIOSFODNN7EXAMPLE is allowlisted,
// AKIA9X2KQ7W3ZP4RT6NB must fire.
func TestSecuritySvcScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := []byte("echo 'AWS_SECRET=\"AKIA9X2KQ7W3ZP4RT6NB\"' > real.txt\n")
	if err := os.WriteFile(filepath.Join(root, "secret.sh"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := (securitySvc{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	hit := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected a hardcoded-secret finding, got %+v", findings)
	}
}

// TestHandleMaskPIISmoke drives handleMaskPII end-to-end: valid args
// produce masked output with the [kern] masked summary line and no leaked
// token.
func TestHandleMaskPIISmoke(t *testing.T) {
	t.Parallel()
	s := &Server{}
	token := "sk-test-abcdefghijklmnopqrstuvwxyz012345"
	out, err := s.handleMaskPII(context.Background(), map[string]any{"text": "token=" + token})
	if err != nil {
		t.Fatalf("handleMaskPII error: %v", err)
	}
	if !strings.Contains(out, "[kern] masked") {
		t.Errorf("output missing [kern] masked summary: %q", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("output still contains the token: %q", out)
	}
}

// TestHandleSchemaValidateSmoke drives handleSchemaValidate with valid data
// and schema, expecting the conforms verdict.
func TestHandleSchemaValidateSmoke(t *testing.T) {
	t.Parallel()
	s := &Server{}
	out, err := s.handleSchemaValidate(context.Background(), map[string]any{
		"data":   `{"name": "test"}`,
		"schema": `{"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}`,
	})
	if err != nil {
		t.Fatalf("handleSchemaValidate error: %v", err)
	}
	if !strings.Contains(out, "schema OK") {
		t.Errorf("expected schema OK verdict, got %q", out)
	}
}

// TestSecurityHandlersRejectMissingArgs verifies each security-family
// handler fails fast with a required-arg error. All these checks run before
// any hook or index access, so a bare &Server{} is safe to exercise.
func TestSecurityHandlersRejectMissingArgs(t *testing.T) {
	t.Parallel()
	s := &Server{}
	ctx := context.Background()
	cases := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"mask_pii", func() (string, error) { return s.handleMaskPII(ctx, map[string]any{}) }, "text is required"},
		{"schema_validate", func() (string, error) { return s.handleSchemaValidate(ctx, map[string]any{}) }, "data and schema are required"},
		{"safe_delete", func() (string, error) { return s.handleSafeDelete(ctx, map[string]any{}) }, "symbol is required"},
		{"verify_output", func() (string, error) { return s.handleVerifyOutput(ctx, map[string]any{}) }, "text is required"},
		{"check_draft", func() (string, error) { return s.handleCheckDraft(ctx, map[string]any{}) }, "code is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.call()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
