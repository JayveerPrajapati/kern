package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

func TestRenderInToto(t *testing.T) {
	rcpt := &Receipt{
		SchemaVersion:  SchemaVersion,
		ReceiptID:      "test-receipt-123",
		RepoRoot:       "/workspace/kern",
		BaseRevision:   "a1b2c3d4",
		HeadRevision:   "f6e5d4c3",
		Status:         "PASS",
		ExitCode:       0,
		ValidationHash: "valhash123",
		AuditChainHash: "audithash123",
		AuditRecordID:  "test-receipt-123",
		FindingsCount:  0,
		GeneratedAt:    time.Now().UTC(),
		GeneratedBy:    "kernops-test",
	}
	rcpt.Signature = rcpt.ComputeSignature()

	data, err := RenderInToto(rcpt)
	if err != nil {
		t.Fatalf("RenderInToto failed: %v", err)
	}

	var stmt InTotoStatement
	if err := json.Unmarshal(data, &stmt); err != nil {
		t.Fatalf("unmarshal in-toto JSON failed: %v", err)
	}

	if stmt.Type != "https://in-toto.io/Statement/v0.1" {
		t.Errorf("unexpected _type: %q", stmt.Type)
	}
	if stmt.PredicateType != "https://kernops.dev/attestation/v1" {
		t.Errorf("unexpected predicateType: %q", stmt.PredicateType)
	}
	if stmt.Predicate.ReceiptID != "test-receipt-123" {
		t.Errorf("unexpected receipt_id: %q", stmt.Predicate.ReceiptID)
	}
	if stmt.Predicate.Status != "PASS" {
		t.Errorf("unexpected status: %q", stmt.Predicate.Status)
	}
}

func TestRenderSARIF(t *testing.T) {
	rcpt := &Receipt{
		SchemaVersion:  SchemaVersion,
		ReceiptID:      "sarif-receipt-456",
		RepoRoot:       "/workspace/kern",
		Status:         "WARN",
		ExitCode:       0,
		ValidationHash: "valhash456",
		AuditChainHash: "audithash456",
		GeneratedAt:    time.Now().UTC(),
	}
	rcpt.Signature = rcpt.ComputeSignature()

	findings := []domain.Finding{
		{
			RuleID:   "G1_SECRETS",
			Severity: domain.SeverityBlock,
			Category: domain.CategorySecret,
			File:     "pkg/auth/token.go",
			Line:     42,
			Column:   10,
			Message:  "Hardcoded token detected",
		},
		{
			RuleID:   "G6_DUPLICATION",
			Severity: domain.SeverityWarn,
			Category: domain.CategoryDuplication,
			File:     "pkg/calc/math.go",
			Line:     15,
			Column:   1,
			Message:  "Clone block detected",
		},
	}

	data, err := RenderSARIF(rcpt, findings)
	if err != nil {
		t.Fatalf("RenderSARIF failed: %v", err)
	}

	var sarifDoc map[string]any
	if err := json.Unmarshal(data, &sarifDoc); err != nil {
		t.Fatalf("unmarshal SARIF JSON failed: %v", err)
	}

	if sarifDoc["version"] != "2.1.0" {
		t.Errorf("expected SARIF version 2.1.0, got %v", sarifDoc["version"])
	}

	runs, ok := sarifDoc["runs"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatalf("expected at least 1 run in SARIF")
	}

	run0 := runs[0].(map[string]any)
	results := run0["results"].([]any)
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestVerifyDiffIntegrity(t *testing.T) {
	tmpDir := t.TempDir()

	rcpt := &Receipt{
		SchemaVersion:  SchemaVersion,
		ReceiptID:      "diff-test",
		RepoRoot:       tmpDir,
		Status:         "PASS",
		AuditChainHash: "dummy-audit-hash",
		GeneratedAt:    time.Now().UTC(),
		HeadRevision:   "HEAD",
	}
	rcpt.Signature = rcpt.ComputeSignature()

	// Clean directory (not git or clean) passes
	if err := VerifyDiffIntegrity(rcpt, tmpDir); err != nil {
		t.Errorf("expected verify to pass on clean dir: %v", err)
	}

	// Tampered signature fails
	tamperedRcpt := *rcpt
	tamperedRcpt.Signature = "tampered-bad-signature"
	if err := VerifyDiffIntegrity(&tamperedRcpt, tmpDir); err == nil {
		t.Errorf("expected verify to fail on tampered signature")
	}

	// Uncommitted dirty file in simulated git status
	_ = os.WriteFile(filepath.Join(tmpDir, "dirty.txt"), []byte("bad"), 0o644)
	// (note: git status won't fail here if not a git repo, but signature check is tested)
	_ = strings.TrimSpace("")
}
