// Perf SLI gate: per-check wall-clock budgets for the diff-gate /
// blueprint check runner.
//
// Every check result carries its measured Duration (set in runCheck); a check
// that blows its budget gets an advisory WARN perf:sli finding and its status
// is bumped PASS -> WARN so the regression is visible in the status line and
// the summary counts — but NEVER to BLOCK: wall-clock timing is
// machine-dependent and flaky by nature, so this gate is an
// order-of-magnitude regression detector, not a precision stopwatch.
//
// Budgets are deliberately generous. Measured steady-state on the kern repo:
// every check <= 1s except secret:scan's cold whole-repo scan (~6s — now
// ~0ms warm after the content-hash cache). Raising a budget is a deliberate
// act documented in the diff that touches this file.
package service

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// perfSLIBudgets maps check names to their wall-clock budget in milliseconds.
// Checks without an entry fall back to perfSLIDefaultBudgetMs.
var perfSLIBudgets = map[string]int64{
	// Cold whole-repo kern sec scan is legitimately seconds; the cache makes
	// warm runs ~0ms. 15s catches an order-of-magnitude regression without
	// false-flagging a cold first run.
	"secret:scan": 15_000,
}

// perfSLIDefaultBudgetMs applies to every check without a specific budget.
const perfSLIDefaultBudgetMs = 5_000

// applyPerfSLI appends a WARN perf:sli finding when the check blew its
// wall-clock budget, and bumps a PASS to WARN so the regression is visible in
// the status line. Exit code is unaffected (WARN is advisory). Applied AFTER
// policy evaluation so a policy-rewritten result still carries the perf
// finding.
func applyPerfSLI(cr *domain.CheckResult) {
	budget := int64(perfSLIDefaultBudgetMs)
	if b, ok := perfSLIBudgets[cr.Name]; ok {
		budget = b
	}
	if cr.Duration <= budget {
		return
	}
	cr.Findings = append(cr.Findings, domain.Finding{
		RuleID:       "perf:sli",
		Severity:     domain.SeverityWarn,
		Category:     domain.CategoryPerformance,
		Message:      fmt.Sprintf("check took %dms, budget %dms", cr.Duration, budget),
		Explanation:  "A diff-gate/blueprint check regressed past its wall-clock budget; the perf:sli finding flags it for optimization.",
		SuggestedFix: "Isolate the check and profile it, then optimize the hot path or deliberately raise its budget in internal/blueprint/service/perfsli.go.",
		RuleVersion:  "1",
		Confidence:   1.0,
		Scope:        "check",
		Evidence: []domain.Evidence{{
			Kind:        "duration",
			Description: fmt.Sprintf("measured %dms vs budget %dms", cr.Duration, budget),
		}},
	})
	if cr.Status == domain.StatusPass {
		cr.Status = domain.StatusWarn
	}
}
