package approvalcli

import "fmt"

// runReject implements `kern reject <id> [--reason ...] [--approver ...]`.
// It mirrors runApprove's two-store resolution exactly — governance store
// first (via the app layer, which advances a gated task to REJECTED and
// writes the audit chain), then the blueprint two-person-rule store — so
// every approval `kern approve` lists can be rejected from the same command
// surface, and an id found in neither store fails loudly (rc=1).
//
// (The former cmd/kern cmd_reject.go additionally carried an init() that
// re-bound the dispatch table's "reject" entry away from the bpcli
// single-store runner; the extraction wires approvalcli.RunReject directly
// in the table, so no init() is needed.)
func runReject(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("reject requires an approval id: kern reject <id> [--reason ...]")
	}
	id := args[0]
	approver := f.approver
	if approver == "" {
		approver = "cli-user"
	}

	// Shared with runApprove: same store order (governance via the app layer,
	// then blueprint), same decided-state guard (rc=3), same audit chaining.
	a, mem := decideApproval(root, "reject", id, approver, false, f.reason)
	fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
	if a.TaskID != "" {
		fmt.Printf("  task: %s marked REJECTED\n", a.TaskID)
	}
	// Same bounded, post-confirmation learning pass as runApprove: the
	// decision is printed first; learning never holds up the exit.
	if mem != nil {
		recordPolicySignalsBounded(root, mem)
	}
}
