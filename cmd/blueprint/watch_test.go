package main

import (
	"context"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	"github.com/JayveerPrajapati/kern/internal/blueprint/watcher"
)

// recordingCheck is a service.Check test double that records the ChangeRequest
// it was asked to validate and returns a predetermined result (mirroring the
// fakeCheck pattern in internal/blueprint/service/validate_test.go).
type recordingCheck struct {
	req    domain.ChangeRequest
	result domain.CheckResult
	err    error
}

func (f *recordingCheck) Name() string { return "test:recording" }

func (f *recordingCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	f.req = req
	return f.result, f.err
}

// TestWatch_ValidateBatchBuildsRequest verifies that validateBatch builds a
// ChangeRequest with one FileChange per distinct event path and surfaces a
// blocking finding.
func TestWatch_ValidateBatchBuildsRequest(t *testing.T) {
	fake := &recordingCheck{
		result: domain.CheckResult{
			Name:   "test:recording",
			Status: domain.StatusBlock,
			Findings: []domain.Finding{
				{RuleID: "test:block", Severity: domain.SeverityBlock, File: "a.go", Line: 1, Message: "block"},
			},
		},
	}
	events := []watcher.Event{
		{Type: watcher.EventModify, Path: "a.go"},
		{Type: watcher.EventCreate, Path: "b.go"},
	}

	hadBlock, findings := validateBatch(context.Background(), "/tmp/repo", []service.Check{fake}, policy.NewEngine(policy.DefaultConfig().Policy), events)

	if !hadBlock {
		t.Error("hadBlock = false, want true")
	}
	if len(findings) != 1 {
		t.Errorf("len(findings) = %d, want 1", len(findings))
	}
	if fake.req.RepositoryRoot != "/tmp/repo" {
		t.Errorf("RepositoryRoot = %q, want %q", fake.req.RepositoryRoot, "/tmp/repo")
	}
	if fake.req.Source != domain.SourceWatch {
		t.Errorf("Source = %q, want %q", fake.req.Source, domain.SourceWatch)
	}
	if fake.req.Operation != domain.OpCommit {
		t.Errorf("Operation = %q, want %q", fake.req.Operation, domain.OpCommit)
	}
	if len(fake.req.Files) != 2 {
		t.Fatalf("len(req.Files) = %d, want 2 (one per distinct event path)", len(fake.req.Files))
	}
	for _, fc := range fake.req.Files {
		if fc.Op != domain.OpEdit {
			t.Errorf("FileChange %q op = %q, want %q", fc.Path, fc.Op, domain.OpEdit)
		}
	}
}

// TestWatch_ValidateBatchClean verifies that a clean batch reports no blocks
// and no findings.
func TestWatch_ValidateBatchClean(t *testing.T) {
	fake := &recordingCheck{
		result: domain.CheckResult{Name: "test:recording", Status: domain.StatusPass},
	}
	events := []watcher.Event{
		{Type: watcher.EventModify, Path: "a.go"},
	}

	hadBlock, findings := validateBatch(context.Background(), "/tmp/repo", []service.Check{fake}, policy.NewEngine(policy.DefaultConfig().Policy), events)

	if hadBlock {
		t.Error("hadBlock = true, want false")
	}
	if len(findings) != 0 {
		t.Errorf("len(findings) = %d, want 0", len(findings))
	}
}

// TestWatch_ValidateBatchAppliesPolicy verifies that the watcher's validation
// path constructs the service WITH a policy engine: a reviewed suppression
// downgrades a BLOCK finding to INFO (Suppressed flag set) so it can never
// block, and mode: warn downgrades the enforced status.
func TestWatch_ValidateBatchAppliesPolicy(t *testing.T) {
	fake := &recordingCheck{
		result: domain.CheckResult{
			Name:   "test:recording",
			Status: domain.StatusBlock,
			Findings: []domain.Finding{
				{RuleID: "test:block", Severity: domain.SeverityBlock, File: "a.go", Line: 1, Message: "block"},
			},
		},
	}
	events := []watcher.Event{
		{Type: watcher.EventModify, Path: "a.go"},
	}

	// Suppression path: a reviewed, unexpired suppression for the finding's
	// rule+file must downgrade BLOCK -> INFO and clear the block signal.
	engine := policy.NewEngine(policy.Policy{
		Mode: "enforce",
		Suppressions: []policy.Suppression{{
			RuleID:   "test:block",
			File:     "a.go",
			Reason:   "reviewed",
			Reviewer: "t",
			Expires:  time.Now().Add(24 * time.Hour),
		}},
	})

	hadBlock, findings := validateBatch(context.Background(), "/tmp/repo", []service.Check{fake}, engine, events)

	if hadBlock {
		t.Error("hadBlock = true, want false (suppressed finding must not block)")
	}
	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1", len(findings))
	}
	if !findings[0].Suppressed {
		t.Error("finding.Suppressed = false, want true (policy suppression applied)")
	}
	if findings[0].Severity != domain.SeverityInfo {
		t.Errorf("finding.Severity = %q, want %q (suppressed -> INFO)", findings[0].Severity, domain.SeverityInfo)
	}
	if findings[0].SuppressionReason != "reviewed" {
		t.Errorf("finding.SuppressionReason = %q, want %q", findings[0].SuppressionReason, "reviewed")
	}
}

// TestWatch_PolicyParsing verifies the --policy → checks mapping. It works
// without a running daemon: duplication needs no kern client, kern-backed
// policies are nil-client errors, and the full wiring (with a real client on
// PATH) yields 2 checks by default and 3 with duplication.
func TestWatch_PolicyParsing(t *testing.T) {
	// duplication needs no kern client: works with a nil client.
	checks, err := buildWatchChecks("duplication", nil)
	if err != nil {
		t.Fatalf("buildWatchChecks(duplication, nil): %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("len(checks) = %d, want 1", len(checks))
	}
	if checks[0].Name() != "duplication:jscpd" {
		t.Errorf("check name = %q, want %q", checks[0].Name(), "duplication:jscpd")
	}

	// Unknown policy name → error.
	if _, err := buildWatchChecks("architecture,bogus", nil); err == nil {
		t.Error("unknown policy name: expected error, got nil")
	}

	// Kern-backed policies require a client.
	if _, err := buildWatchChecks("architecture", nil); err == nil {
		t.Error("architecture with nil client: expected error, got nil")
	}
	if _, err := buildWatchChecks("secrets", nil); err == nil {
		t.Error("secrets with nil client: expected error, got nil")
	}

	// Full wiring with a real client: default = 2 checks, +duplication = 3.
	client, err := kern.NewKernClient()
	if err != nil {
		t.Skipf("kern binary not available; skipping client-dependent assertions: %v", err)
	}

	checks, err = buildWatchChecks("", client) // default architecture,secrets
	if err != nil {
		t.Fatalf("buildWatchChecks(default): %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("default len(checks) = %d, want 2", len(checks))
	}

	checks, err = buildWatchChecks("architecture,secrets,duplication", client)
	if err != nil {
		t.Fatalf("buildWatchChecks(architecture,secrets,duplication): %v", err)
	}
	if len(checks) != 3 {
		t.Fatalf("len(checks) = %d, want 3", len(checks))
	}
}
