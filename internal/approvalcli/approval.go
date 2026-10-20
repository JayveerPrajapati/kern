package approvalcli

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
// --reject`). Sub-dispatch mirrors audit.go's "audit append/repair"
// pattern: the first positional selects the subcommand, the rest is passed
// to it. Bare `kern approval` (or an unknown subcommand) is a usage error.
func runApproval(rest []string) int {
	if len(rest) > 0 {
		switch rest[0] {
		case "wait":
			return runApprovalWait(rest[1:])
		case "clear":
			return runApprovalClear(rest[1:])
		}
	}
	fatalUsage("approval requires a subcommand: kern approval wait <id> [--timeout 30m] [--root R] | kern approval clear <id> [--reason R] [--root R] — see 'kern approval --help'")
	return 0 // unreachable (fatalUsage panics with the exitError sentinel)
}

// runApprovalClear implements `kern approval clear <id> [--reason R]
// [--root R]` — the operator-side escape hatch for approval records that
// block future work without being decidable anymore. It removes the record
// (whatever its status) from the governance store in one atomic mutation
// and appends a tamper-evident audit-chain entry, so a REJECTED record
// stops blocking byte-identical retries of its command (rejection
// stickiness) and a stray PENDING record leaves `kern approve list`.
// Clearing never grants anything — the next gated attempt mints a FRESH
// approval a human still has to decide — which is why clear, like reject,
// stays non-interactive: every direction is deny-or-re-arm, and the
// dangerous direction (granting) keeps its interactive typed-id gate in
// `kern approve`.
func runApprovalClear(rest []string) int {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("approval clear requires an approval id: kern approval clear <id> [--reason R] [--root R] — rejected ids: see the denial message or `kern audit`; pending ids: `kern approve list`")
	}
	id := args[0]
	actor := f.approver
	if actor == "" {
		actor = "cli-user"
	}
	store := governance.NewFileStore(root)
	if err := store.LoadError(); err != nil {
		fatal("approval clear: %v", err)
	}
	a, err := store.Clear(id, actor, f.reason)
	if err != nil {
		fatal("approval clear: %v — pending ids: `kern approve list`", err)
	}
	status := a.Status
	if status == "" {
		status = "pending"
	}
	fmt.Printf("cleared: %s\n", a.ID)
	fmt.Printf("  status: %s\n", status)
	if a.TaskID != "" {
		fmt.Printf("  task: %s\n", a.TaskID)
	}
	if a.DecidedAt != nil {
		fmt.Printf("  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	fmt.Printf("  by: %s\n", actor)
	if status == "rejected" {
		fmt.Printf("  effect: byte-identical retries of the gated command now mint a fresh approval (a human still has to decide it)\n")
	}
	return 0
}

// runApprovalWait blocks until the approval with the given ID is decided,
// polling the two stores `kern approve` resolves — the governance store
// (<root>/.kern/approvals.json, appr-* ids) and the blueprint two-person-rule
// store (<root>/.blueprint/approvals/requests.jsonl, apr-* ids written by
// `kern request-approval`) — on a ~1s ticker. It is the blocking observer
// for a pending approval: an agent that hit a "resolve with: kern approve
// <id>" denial can await the human decision instead of relying on manual
// relay. It prints the current state once at start ("pending: <id>"), then
// the decision when it lands.
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

	govStore := governance.NewFileStore(root)
	fmt.Printf("pending: %s\n", id)

	// Fail fast on an unknown id: the wait is for an approval the caller was
	// already told about (e.g. via the exec deny message or `kern
	// request-approval`'s "A human must run: kern approve <id>" output), so
	// a miss in BOTH stores is a real error, not a reason to block for the
	// full timeout. Store loads are cheap (two small files), so the poll
	// loop re-reads them on every tick and observes decisions made by any
	// process.
	inGov := false
	if _, err := govStore.Get(id); err == nil {
		inGov = true
	}
	if !inGov {
		// A miss is either a wrong id or an approval that was already
		// decided AND consumed (the single-use governance grant is removed
		// from its store once a retry passed with it). Both mean waiting is
		// pointless — fail fast, but say which is which so the caller is
		// not left guessing whether to re-run the original command.
		fatal("approval wait: approval not found: %s — the id is wrong, or the approval was already consumed by a successful retry (re-run the original command; it will pass without a new approval); pending ids: `kern approve list`", id)
	}

	ticker := time.NewTicker(approvalWaitPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		if inGov {
			if a, err := govStore.Get(id); err == nil && a.DecidedAt != nil {
				return printApprovalDecision(a)
			}
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
