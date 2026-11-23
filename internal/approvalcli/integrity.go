package approvalcli

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// integrityNote renders a failing approval-integrity verdict for a human-facing
// listing. A record whose MAC was computed under a since-rotated key and a
// record whose content was hand-edited are different facts; reporting both as
// "modified outside the approval workflow" accuses an untouched record of
// tampering and sends the operator hunting for an attacker instead of a key.
func integrityNote(rep governance.IntegrityReport) string {
	switch rep.Verdict {
	case governance.IntegrityKeyRotated:
		return "!! UNVERIFIABLE (key-rotated): " + rep.Reason + " — stale entries: `kern approval clear --stale`"
	case governance.IntegritySignatureUnverifiable:
		return "!! UNVERIFIABLE (signature): " + rep.Reason
	default:
		// A tampered verdict is NEVER softened, whatever reason it carries.
		msg := "!! INTEGRITY (tampered): " + rep.Reason
		if !strings.Contains(msg, "DO NOT APPROVE") {
			msg += " — DO NOT APPROVE"
		}
		return msg
	}
}
