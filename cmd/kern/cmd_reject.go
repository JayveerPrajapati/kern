package main

import (
	"fmt"
)

// init re-binds the standalone `reject` dispatch entry to runReject. The
// bpcli runner it replaces (`kern blueprint reject` still uses it) resolves
// only the blueprint two-person-rule store, so `kern reject <id>` could not
// decide approvals that `kern approve` lists from the governance store —
// two approval stores out of sync (QA-verified finding). Rejecting must
// resolve the id against the SAME stores runApprove uses, in the same order,
// with the same decided-state guard; the shared core lives in cmd_approve.go
// (decideApproval).
func init() {
	commandTable["reject"] = commandEntry{
		run: func(cmd string, rest []string) int {
			runReject(rest)
			return 0
		},
		help:     "reject a pending approval request: reject <id> [--reason ...]",
		usage:    "usage: kern reject [flags]\n  options:\n    --approver         approver identity\n    --reason           reason text\n    --root             project root (default: .)",
		category: "governance",
	}
}

// runReject implements `kern reject <id> [--reason ...] [--approver ...]`.
// It mirrors runApprove's two-store resolution exactly — governance store
// first (via the app layer, which advances a gated task to REJECTED and
// writes the audit chain), then the blueprint two-person-rule store — so
// every approval `kern approve` lists can be rejected from the same command
// surface, and an id found in neither store fails loudly (rc=1).
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
	a, req := decideApproval(root, "reject", id, approver, false, f.reason)
	if req != nil {
		printBlueprintDecision(req, approver, true)
		return
	}
	fmt.Printf("rejected: %s (by %s)\n", a.ID, approver)
	if a.TaskID != "" {
		fmt.Printf("  task: %s marked REJECTED\n", a.TaskID)
	}
}
