package approvalcli

import (
	"github.com/JayveerPrajapati/kern/internal/approvalkeycli"
)

// runApprovalKey delegates `kern approval key <status|rotate>` to its own
// package: the key lifecycle keeps the approval command surface from accreting
// (the ledger ratchet) and is testable without a terminal.
func runApprovalKey(rest []string) int {
	return approvalkeycli.Run(rest, approvalkeycli.Hooks{
		Actor:      "cli-user",
		Passphrase: approvalPassphrase,
		Audit: func(root, verb, id, actor, result, detail string) {
			recordApprovalAttemptAuditVerb(root, verb, id, actor, result, detail)
		},
	})
}
