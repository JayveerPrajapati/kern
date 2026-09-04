package receipt

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// testResult returns a representative PASS ValidationResult exercising every
// field a receipt pins.
func testResult() domain.ValidationResult {
	return domain.ValidationResult{
		Status:        domain.StatusPass,
		ExitCode:      0,
		CorrelationID: "bp-42",
		DurationMs:    17,
		Summary:       domain.Summary{Total: 1, Warnings: 1},
		Findings: []domain.Finding{{
			RuleID:   "dup:code",
			Severity: domain.SeverityWarn,
			Category: domain.CategoryDuplication,
			File:     "a.go",
			Line:     3,
			Message:  "m",
		}},
		Checks: []domain.CheckResult{{Name: "arch", Status: domain.StatusPass, Duration: 5}},
	}
}

// TestGenerateSealsAllFields: Generate pins the validation outcome, the audit
// chain endpoint, the kern chain hash, and a signature over all of it.
func TestGenerateSealsAllFields(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "feature", "auditchain123", "kernchain456")

	if r.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if r.ReceiptID != "bp-42" || r.AuditRecordID != "bp-42" {
		t.Errorf("receipt_id/audit_record_id = %q/%q, want bp-42/bp-42", r.ReceiptID, r.AuditRecordID)
	}
	if r.RepoRoot != "/tmp/repo" || r.BaseRevision != "main" || r.HeadRevision != "feature" {
		t.Errorf("repo/base/head = %q/%q/%q", r.RepoRoot, r.BaseRevision, r.HeadRevision)
	}
	if r.Status != "PASS" || r.ExitCode != 0 {
		t.Errorf("status/exit_code = %s/%d, want PASS/0", r.Status, r.ExitCode)
	}
	if r.ValidationHash == "" {
		t.Error("validation_hash is empty")
	}
	if r.AuditChainHash != "auditchain123" {
		t.Errorf("audit_chain_hash = %q, want auditchain123", r.AuditChainHash)
	}
	if r.KernChainHash != "kernchain456" {
		t.Errorf("kern_chain_hash = %q, want kernchain456", r.KernChainHash)
	}
	if r.FindingsCount != 1 {
		t.Errorf("findings_count = %d, want 1", r.FindingsCount)
	}
	if r.GeneratedBy != "blueprint-ci" {
		t.Errorf("generated_by = %q, want blueprint-ci", r.GeneratedBy)
	}
	if r.GeneratedAt.IsZero() {
		t.Error("generated_at is zero")
	}
	if r.Signature == "" {
		t.Error("signature is empty")
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("fresh receipt must verify: %v", err)
	}
}

// TestVerify_ValidReceipt: an untouched receipt verifies and its signature
// recomputes to itself.
func TestVerify_ValidReceipt(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	if err := r.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.ComputeSignature() != r.Signature {
		t.Error("ComputeSignature() != Signature on a valid receipt")
	}
}

// TestVerify_TamperedField: changing any sealed field breaks Verify.
func TestVerify_TamperedField(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	tampered := *r
	tampered.Status = "FAIL"
	if err := tampered.Verify(); err == nil {
		t.Fatal("Verify on receipt with tampered status = nil, want error")
	}
}

// TestVerify_TamperedSignature: flipping one hex char of the signature breaks
// Verify.
func TestVerify_TamperedSignature(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	tampered := *r
	last := tampered.Signature[len(tampered.Signature)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	tampered.Signature = tampered.Signature[:len(tampered.Signature)-1] + string(replacement)
	if err := tampered.Verify(); err == nil {
		t.Fatal("Verify on receipt with tampered signature = nil, want error")
	}
}

// TestVerify_SchemaMismatch: an unknown schema version fails Verify even when
// the signature would recompute.
func TestVerify_SchemaMismatch(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	bad := *r
	bad.SchemaVersion = 999
	if err := bad.Verify(); err == nil {
		t.Fatal("Verify on schema mismatch = nil, want error")
	}
}

// TestVerify_EmptySignature: a receipt without a signature is rejected.
func TestVerify_EmptySignature(t *testing.T) {
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	bad := *r
	bad.Signature = ""
	if err := bad.Verify(); err == nil {
		t.Fatal("Verify on signature-less receipt = nil, want error")
	}
}

// TestGenerate_GeneratedBy (Low fix): Generate defaults to "blueprint-ci" for
// backward compatibility with existing callers, and honors an explicit
// generator (e.g. "blueprint-check") when one is supplied — so the struct
// comment's "blueprint-ci" | "blueprint-check" contract is actually reachable.
func TestGenerate_GeneratedBy(t *testing.T) {
	// Default: no variadic arg → historical value preserved.
	r := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "")
	if r.GeneratedBy != "blueprint-ci" {
		t.Errorf("default GeneratedBy = %q, want blueprint-ci", r.GeneratedBy)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("default receipt must verify: %v", err)
	}

	// Explicit generator.
	check := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "", "blueprint-check")
	if check.GeneratedBy != "blueprint-check" {
		t.Errorf("GeneratedBy = %q, want blueprint-check", check.GeneratedBy)
	}
	if err := check.Verify(); err != nil {
		t.Fatalf("blueprint-check receipt must verify: %v", err)
	}
	if check.Signature == r.Signature {
		t.Error("receipts with different generated_by must have different signatures")
	}

	// Empty variadic value falls back to the default.
	empty := Generate(testResult(), "/tmp/repo", "main", "HEAD", "auditabc", "", "")
	if empty.GeneratedBy != "blueprint-ci" {
		t.Errorf("GeneratedBy with empty override = %q, want blueprint-ci", empty.GeneratedBy)
	}
}
