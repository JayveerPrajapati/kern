package service

import (
	"context"
	"strings"
	"testing"
)

const secretText = "the password is sk-1234567890abcdef and the email is alice@example.com"

// TestSecurityMask verifies Mask replaces secrets with [MASKED_*] placeholders
// and reports the replacement count.
func TestSecurityMask(t *testing.T) {
	svc := New()

	res, err := svc.Security.Mask(context.Background(), secretText, nil)
	if err != nil {
		t.Fatalf("Mask: %v", err)
	}
	if res.Replaced == 0 {
		t.Error("expected at least one secret masked")
	}
	if strings.Contains(res.Text, "sk-1234567890abcdef") {
		t.Errorf("masked text still contains the secret: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[MASKED_") {
		t.Errorf("masked text has no [MASKED_*] placeholder: %q", res.Text)
	}
}

// TestSecuritySchemaValidate verifies SchemaValidate accepts conforming data
// and reports violations for non-conforming data.
func TestSecuritySchemaValidate(t *testing.T) {
	svc := New()
	ctx := context.Background()
	spec := `{"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}}`

	violations, err := svc.Security.SchemaValidate(ctx, []byte(`{"name": "kern"}`), spec)
	if err != nil {
		t.Fatalf("SchemaValidate: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected conforming data to pass, got violations: %v", violations)
	}

	violations, err = svc.Security.SchemaValidate(ctx, []byte(`{"age": 1}`), spec)
	if err != nil {
		t.Fatalf("SchemaValidate: %v", err)
	}
	if len(violations) == 0 {
		t.Error("expected non-conforming data to produce violations")
	}
}

// TestSecurityScanEmpty verifies Scan works on a clean temp root and returns
// no findings.
func TestSecurityScanEmpty(t *testing.T) {
	root := t.TempDir()
	svc := New()

	findings, err := svc.Security.Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings on empty root, got %d", len(findings))
	}
	if out := svc.Security.Render(findings, 100); out != "" {
		t.Errorf("Render of empty findings should be empty, got %q", out)
	}
}
