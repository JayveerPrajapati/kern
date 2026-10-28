package claims

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/govaudit"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

const (
	auditClaimID = "audit-chain-integrity"
	auditClaim   = "the tamper-evident audit chain verifies end to end"
)

// claimAuditChain proves the governance claim on the repo's own audit store.
// The failure it guards against is the 2026-10-26 state: every command printed
// a tamper warning because a supported append path (a custom entry ID) broke
// the chain. A repo with no audit store has nothing to verify and skips — a
// missing chain is not evidence of tampering.
func claimAuditChain(root string) Result {
	dir := filepath.Join(root, ".kern", "audit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return skip(auditClaimID, auditClaim, "no audit store at .kern/audit — nothing to verify in this repo")
	}
	l := govaudit.NewAuditLog().
		WithStore(storage.NewLog(dir)).
		WithLockPath(filepath.Join(dir, ".lock")).
		WithMirror(govaudit.MirrorFor(dir))
	if _, err := l.Replay(); err != nil {
		return fail(auditClaimID, auditClaim, "replay failed: "+err.Error())
	}
	tiers := l.ChainTierReport()
	brk, verified := l.VerifyChainReport()
	summary := fmt.Sprintf("%d entries, %d keyed, %d verified", tiers.Total, tiers.Keyed, verified)
	if brk >= 0 {
		// A break fully explained by a format downgrade is an
		// environment-degraded write (the HMAC secret was unavailable at
		// write time), not tampering. Report it as a skip with the exact
		// reason rather than a red gate the operator cannot act on.
		if tiers.DowngradeExplainsBreak() {
			return skip(auditClaimID, auditClaim,
				fmt.Sprintf("chain break at entry %d is explained by an unkeyed-format downgrade (%d degraded)", brk+1, tiers.TailDowngraded))
		}
		return fail(auditClaimID, auditClaim,
			fmt.Sprintf("chain broken at entry %d of %d", brk+1, tiers.Total),
			fmt.Sprintf("integrity mode: %s; keyed=%d plain=%d legacy=%d downgraded=%d",
				l.IntegrityMode(), tiers.Keyed, tiers.Plain, tiers.Legacy, tiers.Downgraded))
	}
	evidence := []string{fmt.Sprintf("integrity mode: %s; keyed=%d plain=%d legacy=%d", l.IntegrityMode(), tiers.Keyed, tiers.Plain, tiers.Legacy)}
	if tiers.Unkeyed() > 0 {
		// Ordering is proven, authenticity is not: say so instead of letting a
		// green line imply keyed provenance it does not have.
		evidence = append(evidence, fmt.Sprintf("%d entries verify only under the unkeyed formula (ordering proven, authenticity not)", tiers.Unkeyed()))
	}
	return pass(auditClaimID, auditClaim, summary, evidence...)
}
