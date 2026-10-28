package claims

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/astrefs"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

const (
	riskClaimID = "impact-risk-contract"
	riskClaim   = "impact never reports low risk for a symbol real code depends on"
)

// claimImpactRisk proves the second half of the blast-radius claim on the
// user-facing `kern impact` path (not just explore): a non-callable symbol
// with AST-verified references must never come back as a bare "low". The
// contract the 2026-10-27 audit established:
//
//	references >= 3 files -> medium or high (never low)
//	references >  0       -> at least the reference evidence is reported
//	no evidence           -> "unknown", never a confident low
//
// "unknown" is an honest answer and always passes; "low" with zero evidence
// for a referenced symbol is the flagship false all-clear and fails.
func claimImpactRisk(root string, ix *index.Index, ixErr error) Result {
	if ixErr != nil {
		return fail(riskClaimID, riskClaim, "index unavailable: "+ixErr.Error())
	}
	targets, err := referenceTargets(ix)
	if err != nil {
		return fail(riskClaimID, riskClaim, "ranking failed: "+err.Error())
	}
	if len(targets) == 0 {
		return skip(riskClaimID, riskClaim, "no referenced non-callable production symbol to exercise")
	}
	p, err := app.NewIndexOnly(root)
	if err != nil {
		return skip(riskClaimID, riskClaim, "index-only platform unavailable: "+err.Error())
	}
	ts := tasklife.NewTaskService(p, eventbus.New())
	var evidence []string
	proven := 0
	lookups := 0
	for _, sym := range targets {
		if proven >= blastProofTargets || lookups >= maxBlastLookups {
			break
		}
		lookups++
		verified := astrefs.Lookup(ix, sym)
		if verified.Count == 0 {
			continue
		}
		q := qualifiedQuery(sym)
		_, rep, _, ierr := ts.Impact(q)
		if ierr != nil {
			evidence = append(evidence, fmt.Sprintf("skipped %s: impact error: %v", q, ierr))
			continue
		}
		files := len(rep.ReferenceFiles)
		// The graph may already answer for a symbol (high/medium from its own
		// callers), in which case the reference safety net deliberately does
		// not attach — that is not a false all-clear. The failure is a "low"
		// verdict: either for a heavily-referenced symbol, or with no evidence
		// at all.
		if rep.Risk == "low" && (files >= 3 || rep.ReferenceCount == 0) {
			return fail(riskClaimID, riskClaim,
				fmt.Sprintf("%s: %d AST-verified references in %d file(s) reported as \"low\" (risk=%s, %d refs surfaced)",
					q, verified.Count, len(verified.Files), rep.Risk, rep.ReferenceCount),
				evidence...)
		}
		proven++
		evidence = append(evidence, fmt.Sprintf("%s: risk=%s, %d refs in %d file(s) (%s) — %s",
			q, rep.Risk, rep.ReferenceCount, files, rep.ReferenceConfidence, rep.RiskDetail))
	}
	if proven == 0 {
		return skip(riskClaimID, riskClaim, "impact could not be exercised on a referenced symbol")
	}
	return pass(riskClaimID, riskClaim,
		fmt.Sprintf("%d impact report(s) grade reference evidence correctly", proven), evidence...)
}
