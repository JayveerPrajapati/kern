package diffgate

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/docbudget"
)

// DocBudgetCheck (G39 — doc:budget) enforces the committed documentation budget manifest
// (docs/doc-budgets.json): every listed document must exist and stay under
// its word ceiling. It BLOCKs on violation — the "one home per fact" guard
// against docs silently growing past their budget.
type DocBudgetCheck struct {
	root string
}

// NewDocBudgetCheck constructs the doc-budget check bound to a repo root.
func NewDocBudgetCheck(root string) *DocBudgetCheck {
	return &DocBudgetCheck{root: root}
}

// Name is the stable check identifier.
func (c *DocBudgetCheck) Name() string { return "doc:budget" }

// Run validates the manifest against the tree.
func (c *DocBudgetCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if c.root == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	violations, err := docbudget.Validate(c.root)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
	}
	if len(violations) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	findings := make([]domain.Finding, 0, len(violations))
	for _, v := range violations {
		detail := v.Message
		if v.Current > 0 && v.Max > 0 {
			detail = fmt.Sprintf("%s (current %d, ceiling %d)", v.Message, v.Current, v.Max)
		}
		findings = append(findings, domain.Finding{
			RuleID:       "doc:budget",
			Severity:     domain.SeverityBlock,
			Category:     domain.CategoryPolicy,
			Message:      detail,
			Explanation:  "docs/doc-budgets.json sets the word ceiling per key document; a listed document must exist and stay under its ceiling.",
			SuggestedFix: "Condense the document or raise its ceiling in docs/doc-budgets.json only when the words need the space.",
			RuleVersion:  "1",
			Confidence:   1.0,
			Scope:        "repo",
			Evidence:     []domain.Evidence{{Kind: "file", Description: v.Path}},
		})
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusBlock, Findings: findings}, nil
}
