package claims

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

const (
	securityClaimID = "security-selfscan"
	securityClaim   = "the repo passes its own security gate with no blocking finding"
)

// claimSecuritySelfScan proves kern's flagship governance claim on kern's own
// tree: `kern verify security` must not report an unsuppressed critical (the
// blocking tier) or high finding. Until 2026-10-27 it did — 6 criticals, 3 of
// them the same false pair (a `.Exec(` SQL call read as a shell exec, plus the
// real dynamic-SQL rule) — which meant the product could not pass the gate it
// sells. The high tier is ratcheted too: it was 9 unsafe-exec warnings that
// fired on every legitimate exec.Command, so the rule is informational now and
// any new high is a genuine escalation.
//
// Suppressed findings are allowed: the registry carries a written reason per
// entry, which is the auditable outcome — but the count is reported so the
// suppression budget stays visible.
func claimSecuritySelfScan(root string, ix *index.Index, ixErr error) Result {
	if ixErr != nil {
		return fail(securityClaimID, securityClaim, "index unavailable: "+ixErr.Error())
	}
	eng := verification.NewEngineWithIndex(root, ix)
	res := eng.VerifySecurity()
	if res == nil {
		return skip(securityClaimID, securityClaim, "security engine unavailable")
	}
	if res.Error != "" {
		return fail(securityClaimID, securityClaim, "scan failed: "+res.Error)
	}
	if !res.OK || res.Critical > 0 {
		detail := fmt.Sprintf("%d unsuppressed critical finding(s)", res.Critical)
		if res.Error != "" {
			detail = res.Error
		}
		return fail(securityClaimID, securityClaim, detail, firstUnsuppressed(res.Findings, "critical")...)
	}
	if res.High > 0 {
		return fail(securityClaimID, securityClaim,
			fmt.Sprintf("%d unsuppressed high finding(s)", res.High),
			firstUnsuppressed(res.Findings, "high")...)
	}
	return pass(securityClaimID, securityClaim,
		fmt.Sprintf("%d findings, 0 critical, 0 high (%d suppressed with reasons)", res.Count, res.Suppressed),
		fmt.Sprintf("informational (low): %d", res.Low))
}

// firstUnsuppressed returns up to five rendered findings of one severity, so a
// failure names the actual sites instead of only a count.
func firstUnsuppressed(findings []verdict.Finding, severity string) []string {
	var out []string
	for _, f := range findings {
		if f.Suppressed || f.Severity != severity {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d %s — %s", f.File, f.Line, f.Rule, f.Message))
		if len(out) == 5 {
			break
		}
	}
	return out
}
