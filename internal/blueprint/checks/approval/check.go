// Package approval implements the two-person approval gate check (P1.3).
//
// The check slots into the validation pipeline as a standard service.Check
// named "approval:gate". It classifies the ChangeRequest's risk and, when the
// risk level requires approval, verifies that an approved request exists in
// the approval store. An unapproved high-risk change returns StatusBlock so
// the aggregate gates the change BEFORE the file checks run (it is wired
// first in the checks slice).
package approval

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/approval"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/risk"
)

// Check implements service.Check for the approval gate.
type Check struct {
	store   *approval.Store
	riskCfg risk.Config
}

// NewCheck constructs the approval gate. The store is created from the
// repository root (.blueprint/approvals/requests.jsonl); cfg tunes risk
// classification and which levels require approval.
func NewCheck(store *approval.Store, cfg risk.Config) *Check {
	return &Check{store: store, riskCfg: cfg}
}

// Name is the stable check identifier used in CheckResult.Name and policy
// routing. Registered as a blocking leg in domain/legs.go.
func (c *Check) Name() string { return "approval:gate" }

// Run classifies the change and enforces the two-person rule:
//
//  1. risk level NOT in RequireApprovalFor  -> PASS (no approval needed)
//  2. risk level requires approval and
//     req.Metadata["approval-id"] is set and resolves to an approved request
//     -> PASS
//  3. otherwise -> BLOCK with an approval:required / approval:rejected
//     finding explaining exactly how to request and attach an approval.
//
// The gate never errors: an unreadable store is treated as "no approval",
// which blocks (fail closed), not fails the pipeline.
func (c *Check) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	// The gate applies to configured sources only (default ["agent"]): a
	// human change is assumed self-reviewed and never gated on its own
	// approval. This check runs before classification — cheap and final.
	if !risk.SourceRequiresApproval(req.Source, c.riskCfg.RequireForSources) {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	assessment := risk.Classify(req, c.riskCfg)
	if !risk.RequiresApproval(assessment.Level, c.riskCfg.RequireApprovalFor) {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	approvalID := req.Metadata["approval-id"]
	if approvalID == "" {
		return c.block("approval:required",
			fmt.Sprintf("high-risk change (level=%s, reasons=[%s]) requires approval. Run `blueprint request-approval --intent %q` to request, then `blueprint approve <id>` to approve, then re-run with `--approval-id <id>`.",
				assessment.Level, strings.Join(assessment.Reasons, "; "), req.Metadata["intent"]),
			assessment), nil
	}

	ar, err := c.store.Get(approvalID)
	if err != nil || ar.Status != approval.StatusApproved {
		// Missing, pending, rejected, or expired: the change stays blocked.
		rule := "approval:required"
		detail := "no approved request found"
		if err == nil {
			switch ar.Status {
			case approval.StatusRejected:
				rule = "approval:rejected"
				detail = fmt.Sprintf("request %s was rejected by %s", approvalID, ar.Approver)
			case approval.StatusPending:
				detail = fmt.Sprintf("request %s is still pending; a human must run `blueprint approve %s`", approvalID, approvalID)
			case approval.StatusExpired:
				detail = fmt.Sprintf("request %s has expired", approvalID)
			}
		}
		return c.block(rule, fmt.Sprintf("high-risk change (level=%s, reasons=[%s]): %s. Re-run with `--approval-id <id>` once approved.",
			assessment.Level, strings.Join(assessment.Reasons, "; "), detail), assessment), nil
	}

	// Approved: gate passes. The approval decision itself is recorded in the
	// audit trail as an approval-decision record when `blueprint approve` ran,
	// so no finding is emitted here (any finding would downgrade the aggregate
	// PASS to WARN under policy, and the gate must pass cleanly).
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
}

// block builds the BLOCK result for an unapproved high-risk change. The
// finding carries SeverityBlock so the policy engine (enforcement "block" for
// the approval category) keeps the result BLOCK — WARN severity would be
// downgraded and the gate would silently pass.
func (c *Check) block(ruleID, msg string, as risk.Assessment) domain.CheckResult {
	f := domain.Finding{
		RuleID:      ruleID,
		Severity:    domain.SeverityBlock,
		Category:    domain.CategoryPolicy,
		Message:     msg,
		Explanation: "The two-person rule (P1.3): high-risk changes need a human-approved request recorded in .blueprint/approvals/requests.jsonl before the file gate passes. Request approval with `blueprint request-approval`, have a human run `blueprint approve <id>`, then re-run with `--approval-id <id>`.",
		RuleVersion: "1",
		Confidence:  1.0,
		Scope:       "repo",
	}
	for _, ind := range as.Indicators {
		f.Evidence = append(f.Evidence, domain.Evidence{
			Kind:        ind.Kind,
			Description: ind.Detail,
			Location:    ind.File,
		})
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusBlock, Findings: []domain.Finding{f}}
}
