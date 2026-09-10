package service

import (
	"context"
	"path/filepath"
	"testing"
)

// TestGovernancePendingApprovalsEmpty verifies PendingApprovals works on a
// fresh root (no store yet) and returns no approvals without erroring.
func TestGovernancePendingApprovalsEmpty(t *testing.T) {
	root := t.TempDir()
	svc := New()

	pending, err := svc.Governance.PendingApprovals(context.Background(), root)
	if err != nil {
		t.Fatalf("PendingApprovals: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending approvals on fresh root, got %d", len(pending))
	}
}

// TestGovernanceApproveUnknownID verifies Approve errors on an unknown
// approval id instead of silently succeeding.
func TestGovernanceApproveUnknownID(t *testing.T) {
	root := t.TempDir()
	svc := New()

	_, err := svc.Governance.Approve(context.Background(), root, "does-not-exist", "tester", true, "")
	if err == nil {
		t.Error("Approve should error on unknown approval id")
	}
}

// TestGovernanceAuditEmpty verifies Audit works on a fresh root and returns no
// entries without erroring (the tamper-evident chain may not exist yet).
func TestGovernanceAuditEmpty(t *testing.T) {
	root := t.TempDir()
	svc := New()

	entries, err := svc.Governance.Audit(context.Background(), root, "")
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no audit entries on fresh root, got %d", len(entries))
	}
}

// TestGovernanceAuditMissingRoot verifies Audit errors when the root
// directory does not exist, so `kern audit --root <missing>` exits non-zero.
// blueprint verify-receipt depends on that exit code to distinguish "chain
// unreadable" (a CI worktree deleted after the run → soft skip + warn) from
// "chain readable but hash absent" (hard failure); before this check a
// missing root returned an empty trail with exit 0 and wrongly invalidated
// receipts whose worktree was cleaned up.
func TestGovernanceAuditMissingRoot(t *testing.T) {
	svc := New()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := svc.Governance.Audit(context.Background(), missing, ""); err == nil {
		t.Fatal("Audit on a missing root should error")
	}
}
