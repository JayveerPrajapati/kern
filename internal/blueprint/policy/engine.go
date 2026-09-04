// Package policy implements Blueprint's policy model: loading config, evaluating
// check results against enforcement rules, and maintaining the monotonic-block
// invariant (spec Section 8).
package policy

import (
	"path"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Rule binds a check category to an enforcement level (spec Section 7).
type Rule struct {
	Category    domain.Category
	Enforcement domain.Enforcement
}

// Policy is the resolved set of enforcement rules plus global mode.
type Policy struct {
	Mode  string // "enforce" | "warn" | "off"
	Rules map[domain.Category]domain.Enforcement
	// SourceRules holds per-source enforcement overrides (spec P0-3), keyed by
	// change source then category. An empty map means no overrides: global
	// Rules apply for every source.
	SourceRules map[domain.Source]map[domain.Category]domain.Enforcement
	// Suppressions holds reviewed, expiring suppressions (P1-2), resolved from
	// .blueprint/suppressions.yaml. The engine applies them before status
	// computation: a matched finding is flagged Suppressed and downgraded to
	// INFO so it can never force a BLOCK.
	Suppressions []Suppression
	// Owners maps rule IDs to the team(s) responsible (P1-2), resolved from
	// .blueprint/owners.yaml. The engine stamps finding.Owner so warnings and
	// blocks become routed work instead of noise.
	Owners map[string][]string
}

// Engine implements service.PolicyEvaluator. It maps raw CheckResult statuses
// to enforced statuses per the loaded Policy, resolving per-source overrides
// (spec P0-3) from the CheckResult.Source stamp.
//
// Monotonic invariant (spec Section 8): if a finding has SeverityBlock and the
// rule is EnforcementBlock, the result Status MUST be StatusBlock. A later
// check can never downgrade this.
type Engine struct {
	policy Policy
}

// NewEngine constructs a policy engine from a resolved Policy.
func NewEngine(p Policy) *Engine {
	return &Engine{policy: p}
}

// Evaluate applies the policy to a CheckResult.
func (e *Engine) Evaluate(result domain.CheckResult) (domain.Status, []domain.Finding) {
	if e.policy.Mode == "off" {
		return domain.StatusSkip, result.Findings
	}

	// Tool errors and skips are not policy decisions and must never be
	// downgraded to PASS. A check that could not run (missing binary, invalid
	// config, parse failure) is a tool failure, not a clean result — preserving
	// it as ERROR ensures Aggregate surfaces it instead of silently passing.
	// Spec Section 8: "A single ERROR forces final ERROR (tool failure is not
	// a silent pass)."
	if result.Status == domain.StatusError || result.Status == domain.StatusSkip {
		return result.Status, result.Findings
	}

	findings := result.Findings
	cat := categoryFromCheck(result.Name)
	enforcement := e.policy.Rules[cat]
	if enforcement == "" {
		// Default: deterministic checks block, probabilistic warn (spec Rule 4).
		enforcement = domain.EnforcementWarn
	}

	// Per-source override (spec P0-3): an explicit override for the change's
	// source replaces the global rule for this category. An empty Source means
	// the change declared no provenance — global rules apply exactly as before.
	if srcRules, ok := e.policy.SourceRules[result.Source]; ok {
		if override, ok := srcRules[cat]; ok {
			enforcement = override
		}
	}

	if e.policy.Mode == "warn" {
		// Warn mode downgrades BLOCK to WARN but never to PASS.
		enforcement = domain.EnforcementWarn
	}

	// P1-2 suppression: a reviewed, expiring suppression is the ONLY
	// sanctioned way to lift a BLOCK, and it requires a reviewer and an
	// expiry. A matched finding is downgraded to INFO (visible, never
	// blocking) and flagged Suppressed, so the suppression itself stays
	// visible in results and audit records. Applied BEFORE status
	// computation: a suppressed block finding no longer forces BLOCK —
	// that is the whole point of a reviewed, expiring suppression. All
	// other findings are untouched (monotonic behavior preserved).
	now := time.Now()
	for i := range findings {
		if s := matchSuppression(e.policy.Suppressions, findings[i], now); s != nil {
			findings[i].Suppressed = true
			findings[i].SuppressionReason = s.Reason
			findings[i].Severity = domain.SeverityInfo
		}
		// RuleID -> owner routing: stamp the responsible team so warnings and
		// blocks become routed work instead of noise.
		if findings[i].Owner == "" {
			findings[i].Owner = ownersFor(e.policy.Owners, findings[i].RuleID)
		}
	}

	status := domain.StatusPass
	switch enforcement {
	case domain.EnforcementBlock:
		for _, f := range findings {
			if f.Severity == domain.SeverityBlock || f.Severity == domain.SeverityError {
				status = domain.StatusBlock
				break
			}
		}
		// If findings exist but none are block-severity, still warn.
		if status == domain.StatusPass && len(findings) > 0 {
			status = domain.StatusWarn
		}
	case domain.EnforcementWarn:
		if len(findings) > 0 {
			status = domain.StatusWarn
		}
	case domain.EnforcementSkip:
		// Skipped enforcement: findings preserved, but the check is not
		// enforced. Any finding makes the result SKIP — never PASS, which
		// preserves the monotonic invariant (spec Section 8) that a finding
		// (especially SeverityBlock) can never be silently downgraded to a
		// clean PASS. A clean check with no findings still passes.
		if len(findings) > 0 {
			status = domain.StatusSkip
		}
	}

	return status, findings
}

// categoryFromCheck maps a check name to its category for policy lookup.
// Checks are named "<category>:<detail>" (e.g. "architecture:guard", "secret:scan").
func categoryFromCheck(name string) domain.Category {
	for i, r := range name {
		if r == ':' {
			return domain.Category(name[:i])
		}
	}
	return domain.Category(name)
}

// matchSuppression returns the first suppression matching the finding, or nil.
// A suppression matches when the rule ids are equal, the finding's file
// matches the suppression's file glob (path.Match semantics; an empty File
// matches any file, and a match error is treated as no match — the loader
// validates globs, so this cannot normally happen), and the suppression has
// not expired.
func matchSuppression(supps []Suppression, f domain.Finding, now time.Time) *Suppression {
	for i := range supps {
		s := &supps[i]
		if s.RuleID != f.RuleID {
			continue
		}
		if s.File != "" {
			ok, err := path.Match(s.File, f.File)
			if err != nil || !ok {
				continue
			}
		}
		if !now.Before(s.Expires) {
			continue
		}
		return s
	}
	return nil
}

// ownersFor joins the configured owners for a rule id into a routing string.
// A single owner is used as-is; multiple owners are joined with ", "; a rule
// id with no configured owner yields "".
func ownersFor(owners map[string][]string, ruleID string) string {
	os := owners[ruleID]
	if len(os) == 0 {
		return ""
	}
	if len(os) == 1 {
		return os[0]
	}
	return strings.Join(os, ", ")
}
