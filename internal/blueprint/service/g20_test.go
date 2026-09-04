package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// fakeBlockCheck is a test double implementing service.Check with a
// predetermined block result. It lives in the external test package because
// service's own test package cannot import policy (policy imports service).
type fakeBlockCheck struct {
	name   string
	result domain.CheckResult
}

func (f *fakeBlockCheck) Name() string { return f.name }
func (f *fakeBlockCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	return f.result, nil
}

// TestG20_SuppressedBlockDoesNotBlock: engine with a suppression + a fake
// check returning a block finding => final status WARN (not BLOCK), and the
// finding is marked suppressed in the aggregated result (P1-2).
func TestG20_SuppressedBlockDoesNotBlock(t *testing.T) {
	engine := policy.NewEngine(policy.Policy{
		Mode: "enforce",
		Rules: map[domain.Category]domain.Enforcement{
			domain.CategorySecret: domain.EnforcementBlock,
		},
		Suppressions: []policy.Suppression{
			{
				RuleID:   "secret:hardcoded-secret",
				Reason:   "placeholder credentials in test fixtures",
				Reviewer: "platform-eng",
				Expires:  time.Now().Add(24 * time.Hour),
			},
		},
		Owners: map[string][]string{
			"secret:hardcoded-secret": {"platform-eng"},
		},
	})
	checks := []service.Check{
		&fakeBlockCheck{name: "secret:scan", result: domain.CheckResult{
			Name:   "secret:scan",
			Status: domain.StatusBlock,
			Findings: []domain.Finding{{
				RuleID:   "secret:hardcoded-secret",
				Severity: domain.SeverityBlock,
				Category: domain.CategorySecret,
				File:     "config/creds.go",
				Message:  "hardcoded secret",
			}},
		}},
	}
	svc := service.New(checks, service.WithPolicy(engine))

	req := domain.ChangeRequest{
		RepositoryRoot: "/tmp/repo",
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpEdit}},
	}
	r := svc.Validate(context.Background(), req)
	if r.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN (suppressed block must not block)", r.Status)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (WARN is not a violation exit)", r.ExitCode)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(r.Findings))
	}
	f := r.Findings[0]
	if !f.Suppressed {
		t.Error("finding.Suppressed = false, want true")
	}
	if f.Severity != domain.SeverityInfo {
		t.Errorf("finding.Severity = %q, want %q", f.Severity, domain.SeverityInfo)
	}
	if f.Owner != "platform-eng" {
		t.Errorf("finding.Owner = %q, want platform-eng (routing stamp)", f.Owner)
	}
}
