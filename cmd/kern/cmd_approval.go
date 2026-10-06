package main

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// approvalWaitPollInterval is how often `kern approval wait` re-reads the
// approval store while blocked on a pending decision.
const approvalWaitPollInterval = 1 * time.Second

// approvalWaitDefaultTimeout is the default --timeout for `kern approval wait`
// when the operator gives none: a waiting agent gets a generous window for
// the human decision without the command blocking forever.
const approvalWaitDefaultTimeout = 30 * time.Minute

// runApproval implements `kern approval <subcommand>` — the approval-family
// surface beyond the decision commands (`kern approve` / `kern approve
// --reject`). Sub-dispatch mirrors cmd_audit.go's "audit append/repair"
// pattern: the first positional selects the subcommand, the rest is passed
// to it. Bare `kern approval` (or an unknown subcommand) is a usage error.
func runApproval(rest []string) int {
	if len(rest) > 0 {
		switch rest[0] {
		case "wait":
			return runApprovalWait(rest[1:])
		}
	}
	fatalUsage("approval requires a subcommand: kern approval wait <id> [--timeout 30m] [--root R] — see 'kern approval --help'")
	return 0 // unreachable (fatalUsage panics with the exitError sentinel)
}

// runApprovalWait blocks until the approval with the given ID is decided,
// polling governance.NewFileStore(root).Get(id) on a ~1s ticker — the store
// at <root>/.kern/approvals.json that `kern approve <id>` resolves. It is
// the blocking observer for a pending approval: an agent that hit a
// "resolve with: kern approve <id>" denial can await the human decision
// instead of relying on manual relay. It prints the current state once at
// start ("pending: <id>"), then the decision when it lands.
//
// Exit codes per the repo contract (see `kern exitcode`):
//   - 0 when the approval was approved;
//   - 3 (denied / policy outcome) when it was rejected;
//   - 1 when --timeout expires before a decision lands (or the id is
//     unknown — a miss is a real error, not a reason to block for the full
//     timeout).
func runApprovalWait(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("approval wait requires an approval id: kern approval wait <id> [--timeout 30m] [--root R]")
	}
	id := args[0]
	timeout := approvalWaitTimeout(f)

	store := governance.NewFileStore(root)
	fmt.Printf("pending: %s\n", id)

	// Fail fast on an unknown id: the wait is for an approval the caller was
	// already told about (e.g. via the exec deny message), so a miss is a
	// real error, not a reason to block for the full timeout. Store loads are
	// cheap (one small JSON file), so the poll loop re-reads on every tick
	// and observes decisions made by any process.
	if _, err := store.Get(id); err != nil {
		// A miss is either a wrong id or an approval that was already
		// decided AND consumed (the single-use grant is removed from the
		// store once a retry passed with it). Both mean waiting is
		// pointless — fail fast, but say which is which so the caller is
		// not left guessing whether to re-run the original command.
		fatal("approval wait: %v — the id is wrong, or the approval was already consumed by a successful retry (re-run the original command; it will pass without a new approval); pending ids: `kern approve list`", err)
	}

	ticker := time.NewTicker(approvalWaitPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		if a, err := store.Get(id); err == nil && a.DecidedAt != nil {
			return printApprovalDecision(a)
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			fmt.Printf("approval %s still pending after %s — timed out\n", id, timeout)
			return 1
		}
	}
}

// approvalWaitTimeout resolves `kern approval wait`'s --timeout: a duration
// string (30m, 500ms) wins, then integer seconds, then the 30m default.
func approvalWaitTimeout(f flags) time.Duration {
	if f.timeoutDur != "" {
		if d, err := time.ParseDuration(f.timeoutDur); err == nil {
			return d
		}
	}
	if f.timeoutSet {
		return time.Duration(f.timeout) * time.Second
	}
	return approvalWaitDefaultTimeout
}

// printApprovalDecision renders the landed decision and maps it to the exit
// code the repo contract assigns: approved → 0, rejected → 3 (denied), any
// other decided state → 1 (present but not classifiable as a grant).
func printApprovalDecision(a domain.Approval) int {
	switch a.Status {
	case "approved":
		fmt.Printf("approved: %s\n", a.ID)
	case "rejected":
		fmt.Printf("rejected: %s\n", a.ID)
	default:
		fmt.Printf("decided: %s (status %s)\n", a.ID, a.Status)
		return 1
	}
	if a.TaskID != "" {
		fmt.Printf("  task: %s\n", a.TaskID)
	}
	if a.Approver != "" {
		fmt.Printf("  approver: %s\n", a.Approver)
	}
	if a.DecidedAt != nil {
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	if a.Status == "approved" {
		return 0
	}
	return 3
}
